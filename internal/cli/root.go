package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/version"
)

// Options holds the global CLI flags and resolved state shared across all commands.
type Options struct {
	// ConfigPath is the --config flag value (defaults to ".dev.toml").
	ConfigPath string
	// DaemonConfig is the --daemon-config flag value.
	DaemonConfig string
	// Debug enables verbose logging when true.
	Debug bool
	// WorkingDir is the process working directory at startup.
	WorkingDir string
	// ResolvedPaths holds the absolute paths computed from the flag values.
	ResolvedPaths ResolvedPaths
}

// ResolvedPaths holds absolute filesystem paths for the project and daemon configs,
// resolved from the CLI flag values during PersistentPreRunE.
type ResolvedPaths struct {
	// ProjectConfig is the absolute path to .dev.toml.
	ProjectConfig string
	// DaemonConfig is the absolute path to daemon.toml.
	DaemonConfig string
}

func Execute() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	opts := &Options{WorkingDir: cwd}
	var showVersion bool

	root := &cobra.Command{
		Use:   "dev",
		Short: "dev",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println(version.String())
				os.Exit(0)
			}
			return resolvePaths(opts)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println(version.String())
				return nil
			}
			return cmd.Help()
		},
	}
	root.SilenceUsage = true

	root.PersistentFlags().StringVar(&opts.ConfigPath, "config", config.DefaultProjectConfig, "override project config path")
	root.PersistentFlags().StringVar(&opts.DaemonConfig, "daemon-config", config.ResolveDaemonConfigPath(), "override daemon config path")
	root.PersistentFlags().BoolVar(&opts.Debug, "debug", false, "enable debug logging")
	root.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "show dev version")

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
	root.AddCommand(newVersionCmd())

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
