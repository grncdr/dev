package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/daemon"
)

const (
	daemonLogPathEnv        = "DEV_DAEMON_LOG"
	maxLogBytes             = 20 * 1024 * 1024
	daemonStartLogTailBytes = 8 * 1024
	daemonStartLogTailLines = 20
)

func newDaemonCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "manage the dev daemon",
	}

	cmd.AddCommand(newDaemonStartCmd(opts))
	cmd.AddCommand(newDaemonStopCmd(opts))
	cmd.AddCommand(newDaemonRestartCmd(opts))
	cmd.AddCommand(newDaemonStatusCmd(opts))
	cmd.AddCommand(newDaemonLogsCmd())
	cmd.AddCommand(newDaemonRunCmd(opts))

	return cmd
}

func newDaemonStartCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(opts)
		},
	}
}

func newDaemonStopCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStop(opts)
		},
	}
}

func newDaemonStatusCmd(opts *Options) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStatus(opts, verbose)
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed status for running worktrees")
	return cmd
}

func newDaemonRestartCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonRestart(opts)
		},
	}
}

func newDaemonRunCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "run",
		Short:  "run the daemon in the foreground",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonRun()
		},
	}
	return cmd
}

func runDaemonStart(opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	if running, err := daemonRunning(socketPath); err != nil {
		return err
	} else if running {
		return errors.New("daemon already running")
	}

	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}

	args := []string{"daemon", "run", "--config", opts.ConfigPath, "--daemon-config", opts.DaemonConfig}
	if opts.Debug {
		args = append(args, "--debug")
	}

	cmd := exec.Command(os.Args[0], args...)
	logFile, logPath, err := openDaemonLog()
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", daemonLogPathEnv, logPath))
	if runtime.GOOS != "windows" {
		sysProc := getSysProcAttr()
		cmd.SysProcAttr = &sysProc
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()

	if err := waitForDaemon(socketPath, logPath, monitorCmdExit(cmd), 3*time.Second); err != nil {
		return err
	}

	fmt.Printf("daemon started (pid %d)\n", cmd.Process.Pid)
	return nil
}

func runDaemonRun() error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	srv, err := daemon.NewServer(socketPath)
	if err != nil {
		return err
	}

	err = srv.Serve()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openDaemonLog() (*os.File, string, error) {
	stateDir, err := config.ResolveStateDir(nil)
	if err != nil {
		return nil, "", err
	}
	logsDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, "", err
	}
	path := filepath.Join(logsDir, "daemon.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	if err := trimFileToMax(file, maxLogBytes); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	return file, path, nil
}

func trimFileToMax(file *os.File, maxBytes int64) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	start := info.Size() - maxBytes
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, maxBytes)
	if _, err := io.ReadFull(file, buf); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(buf); err != nil {
		return err
	}
	return nil
}

func runDaemonStop(opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	if running, err := daemonRunning(socketPath); err != nil {
		return err
	} else if !running {
		fmt.Println("daemon not running")
		return nil
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Shutdown(ctx); err != nil {
		return err
	}

	if err := waitForDaemonStopped(socketPath, 5*time.Second); err != nil {
		return err
	}
	fmt.Println("daemon stopped")
	return nil
}

func runDaemonRestart(opts *Options) error {
	if err := runDaemonStop(opts); err != nil {
		return err
	}
	return runDaemonStart(opts)
}

func runDaemonStatus(opts *Options, verbose bool) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	health, err := client.Health(ctx)
	if err != nil {
		fmt.Println("daemon not running")
		return nil
	}

	version := strings.TrimSpace(health.Version)
	if version == "" {
		version = "unknown"
	}
	fmt.Printf("daemon running (pid %d, version %s)\n", health.PID, version)

	if !verbose {
		return nil
	}

	running, err := client.RunningWorktrees(ctx)
	if err != nil {
		return err
	}
	if running == nil || len(running.Worktrees) == 0 {
		fmt.Println()
		fmt.Println("No running worktrees.")
		return nil
	}

	targets := make([]worktreeStatusRenderTarget, 0, len(running.Worktrees))
	for _, wt := range running.Worktrees {
		slug := strings.TrimSpace(wt.Slug)
		if slug == "" {
			continue
		}
		targets = append(targets, worktreeStatusRenderTarget{
			slug:    slug,
			dirHint: strings.TrimSpace(wt.Path),
			target:  &processTarget{all: true},
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].slug == targets[j].slug {
			return targets[i].dirHint < targets[j].dirHint
		}
		return targets[i].slug < targets[j].slug
	})

	fmt.Println()
	return renderWorktreeDetailedStatuses(client, opts, true, version, targets)
}

func daemonRunning(socketPath string) (bool, error) {
	if _, err := os.Stat(socketPath); err != nil {
		return false, nil
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := client.Health(ctx)
	if err == nil {
		return true, nil
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return false, nil
	}

	return false, nil
}

func ensureDaemonRunning(opts *Options, socketPath string) error {
	if socketPath == "" {
		return errors.New("socket path required")
	}
	if running, err := daemonRunning(socketPath); err != nil {
		return err
	} else if running {
		return nil
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	args := []string{"daemon", "run", "--config", opts.ConfigPath, "--daemon-config", opts.DaemonConfig}
	if opts.Debug {
		args = append(args, "--debug")
	}
	cmd := exec.Command(os.Args[0], args...)
	logFile, logPath, err := openDaemonLog()
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", daemonLogPathEnv, logPath))
	if runtime.GOOS != "windows" {
		sysProc := getSysProcAttr()
		cmd.SysProcAttr = &sysProc
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return waitForDaemon(socketPath, logPath, monitorCmdExit(cmd), 3*time.Second)
}

func monitorCmdExit(cmd *exec.Cmd) <-chan error {
	if cmd == nil {
		return nil
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	return exitCh
}

func waitForDaemon(socketPath, logPath string, exitCh <-chan error, timeout time.Duration) error {
	client := daemon.NewClient(socketPath)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-exitCh:
			if err == nil {
				err = errors.New("daemon exited before becoming healthy")
			}
			return daemonStartFailure(err, lastErr, logPath)
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, err := client.Health(ctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return daemonStartFailure(errors.New("timed out waiting for daemon health check"), lastErr, logPath)
}

func daemonStartFailure(cause, lastErr error, logPath string) error {
	msg := "daemon failed to start"
	if cause != nil {
		msg = fmt.Sprintf("%s: %v", msg, cause)
	}
	if lastErr != nil {
		msg = fmt.Sprintf("%s (last health check: %v)", msg, lastErr)
	}
	logTail, err := readDaemonStartupLogTail(logPath)
	if err != nil {
		return fmt.Errorf("%s; failed to read %s: %w", msg, logPath, err)
	}
	if logTail == "" {
		if strings.TrimSpace(logPath) == "" {
			return errors.New(msg)
		}
		return fmt.Errorf("%s; see %s", msg, logPath)
	}
	return fmt.Errorf("%s\n\nRecent daemon log output from %s:\n%s", msg, logPath, logTail)
}

func readDaemonStartupLogTail(logPath string) (string, error) {
	if strings.TrimSpace(logPath) == "" {
		return "", nil
	}
	data, err := readTailFromPath(logPath, daemonStartLogTailBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return lastLogLines(data, daemonStartLogTailLines), nil
}

func lastLogLines(data []byte, lines int) string {
	if lines <= 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ""
	}
	parts := bytes.Split(trimmed, []byte("\n"))
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return string(bytes.Join(parts, []byte("\n")))
}

func waitForDaemonStopped(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active, err := socketActive(socketPath)
		if err == nil && !active {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon did not stop in time")
}

func socketActive(socketPath string) (bool, error) {
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false, nil
	}
	_ = conn.Close()
	return true, nil
}

func removeStaleSocket(socketPath string) error {
	if _, err := os.Stat(socketPath); err != nil {
		return nil
	}
	return os.Remove(socketPath)
}

func init() {
	_ = filepath.Separator
}
