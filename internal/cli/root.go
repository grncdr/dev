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
	UserConfig    string
	Debug         bool
	ResolvedPaths ResolvedPaths
}

type ResolvedPaths struct {
	ProjectConfig string
	UserConfig    string
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
	root.PersistentFlags().StringVar(&opts.UserConfig, "user-config", config.DefaultUserConfig, "override user config path")
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

	userPath, err := config.ExpandUserPath(opts.UserConfig)
	if err != nil {
		return fmt.Errorf("resolve user config path: %w", err)
	}
	opts.ResolvedPaths.UserConfig = userPath

	return nil
}

func ExitErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
