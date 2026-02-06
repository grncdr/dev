package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dev/internal/config"
)

type Options struct {
	ConfigPath    string
	DaemonConfig  string
	Debug         bool
	WorkingDir    string
	ResolvedPaths ResolvedPaths
}

type ResolvedPaths struct {
	ProjectConfig string
	DaemonConfig  string
}

func Execute() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	opts := &Options{WorkingDir: cwd}

	root := &cobra.Command{
		Use:   "dev",
		Short: "dev",
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
	root.AddCommand(newDocsCmd(opts))
	root.AddCommand(newLogsCmd(opts))
	root.AddCommand(newGatewayCmd(opts))
	root.AddCommand(newShareCmd(opts))
	root.AddCommand(newUnshareCmd(opts))
	root.AddCommand(newWorktreeCmd(opts))
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
	if strings.TrimSpace(opts.WorkingDir) == "" {
		return errors.New("missing working directory")
	}

	projectPath := opts.ConfigPath
	if !filepath.IsAbs(projectPath) {
		projectPath = filepath.Join(opts.WorkingDir, projectPath)
	}
	projectPath = filepath.Clean(projectPath)
	opts.ResolvedPaths.ProjectConfig = projectPath

	daemonPath, err := config.ExpandUserPath(opts.DaemonConfig)
	if err != nil {
		return fmt.Errorf("resolve daemon config path: %w", err)
	}
	opts.ResolvedPaths.DaemonConfig = daemonPath

	return nil
}

func workingDir(opts *Options) string {
	if opts == nil {
		return ""
	}
	return opts.WorkingDir
}

func ExitErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}
