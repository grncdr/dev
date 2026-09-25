package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/daemon"
)

const (
	daemonLogPathEnv = "DEV_DAEMON_LOG"
)

func newDaemonCmd(opts *config.GlobalFlags) *cobra.Command {
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

func newDaemonStartCmd(opts *config.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(opts)
		},
	}
}

func newDaemonStopCmd(opts *config.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "stop the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStop(opts)
		},
	}
}

func newDaemonStatusCmd(opts *config.GlobalFlags) *cobra.Command {
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

func newDaemonRestartCmd(opts *config.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonRestart(opts)
		},
	}
}

func newDaemonRunCmd(opts *config.GlobalFlags) *cobra.Command {
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

func runDaemonStart(opts *config.GlobalFlags) error {
	client, err := daemon.DefaultClient(opts)
	if err != nil {
		return err
	}

	if client.IsRunning() {
		return errors.New("daemon already running")
	}

	err = client.EnsureDaemonRunning(opts)
	if err != nil {
		return err
	}
	fmt.Println("daemon started")
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

func runDaemonStop(opts *config.GlobalFlags) error {
	client, err := daemon.DefaultClient(opts)
	if err != nil {
		return err
	}

	if !client.IsRunning() {
		fmt.Println("daemon not running")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Shutdown(ctx); err != nil {
		return err
	}

	if err := client.WaitForDaemonStopped(5 * time.Second); err != nil {
		return err
	}
	fmt.Println("daemon stopped")
	return nil
}

func runDaemonRestart(opts *config.GlobalFlags) error {
	if err := runDaemonStop(opts); err != nil {
		return err
	}
	return runDaemonStart(opts)
}

func runDaemonStatus(opts *config.GlobalFlags, verbose bool) error {
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

func init() {
	_ = filepath.Separator
}
