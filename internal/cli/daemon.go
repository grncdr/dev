package cli

import (
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
	"time"

	"github.com/spf13/cobra"

	"dev-mode/internal/config"
	"dev-mode/internal/daemon"
)

const (
	daemonLogPathEnv = "DEV_MODE_DAEMON_LOG"
	maxLogBytes      = 20 * 1024 * 1024
)

func newDaemonCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "manage the dev-mode daemon",
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
	return &cobra.Command{
		Use:   "status",
		Short: "show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStatus()
		},
	}
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
		return err
	}

	if err := waitForDaemon(socketPath, 3*time.Second); err != nil {
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

func runDaemonStatus() error {
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

	fmt.Printf("daemon running (pid %d)\n", health.PID)
	return nil
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
		return err
	}
	return waitForDaemon(socketPath, 3*time.Second)
}

func waitForDaemon(socketPath string, timeout time.Duration) error {
	client := daemon.NewClient(socketPath)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, err := client.Health(ctx)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon failed to start")
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
