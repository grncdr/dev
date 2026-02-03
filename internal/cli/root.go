package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"dev-mode/internal/config"
)

type Options struct {
	ConfigPath    string
	DaemonConfig  string
	Debug         bool
	ResolvedPaths ResolvedPaths
}

type ResolvedPaths struct {
	ProjectConfig string
	DaemonConfig  string
}

func Execute() error {
	opts := &Options{}

	root := &cobra.Command{
		Use:   "dev-mode",
		Short: "dev-mode",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return resolvePaths(opts)
		},
	}

	root.PersistentFlags().StringVar(&opts.ConfigPath, "config", config.DefaultProjectConfig, "override project config path")
	root.PersistentFlags().StringVar(&opts.DaemonConfig, "daemon-config", config.ResolveDaemonConfigPath(), "override daemon config path")
	root.PersistentFlags().BoolVar(&opts.Debug, "debug", false, "enable debug logging")

	root.AddCommand(newConfigCmd(opts))
	root.AddCommand(newInitCmd(opts))
	root.AddCommand(newDaemonCmd(opts))
	root.AddCommand(newAttachCmd(opts))
	root.AddCommand(newDNSCmd())
	root.AddCommand(newCertCmd())
	root.AddCommand(newInstallCmd())
	root.AddCommand(newLogsCmd(opts))
	root.AddCommand(newGatewayCmd(opts))
	root.AddCommand(newShareCmd(opts))
	root.AddCommand(newUnshareCmd(opts))
	root.AddCommand(newWorktreeStartCmd(opts))
	root.AddCommand(newWorktreeStopCmd(opts))
	root.AddCommand(newWorktreeRestartCmd(opts))
	root.AddCommand(newWorktreeStatusCmd(opts))

	return root.Execute()
}

func resolvePaths(opts *Options) error {
	if opts == nil {
		return errors.New("missing options")
	}

	projectPath, err := filepath.Abs(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("resolve project config path: %w", err)
	}
	opts.ResolvedPaths.ProjectConfig = projectPath

	daemonPath, err := config.ExpandUserPath(opts.DaemonConfig)
	if err != nil {
		return fmt.Errorf("resolve daemon config path: %w", err)
	}
	opts.ResolvedPaths.DaemonConfig = daemonPath

	return nil
}

func ExitErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
