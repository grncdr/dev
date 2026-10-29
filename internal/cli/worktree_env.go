package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/worktree"
)

var worktreeEnvironmentKeys = []string{
	"DEV_PROJECT",
	"DEV_WORKTREE_SLUG",
	"DEV_WORKTREE_DNS_NAME",
	"DEV_WORKTREE_PATH",
	"DEV_WORKTREE_BRANCH",
}

func newWorktreeEnvCmd(opts *config.GlobalFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "env",
		Short: "print the resolved current-worktree environment",
		Long: "Print the same core worktree environment variables that dev injects into managed processes. " +
			"The default shell output can be loaded with: eval \"$(dev worktree env)\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeEnv(opts, output, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "shell", "output format: shell or json")
	return cmd
}

func runWorktreeEnv(opts *config.GlobalFlags, output string, out io.Writer) error {
	if output != "shell" && output != "json" {
		return fmt.Errorf("unknown output format: %s", output)
	}
	if opts == nil {
		return fmt.Errorf("missing options")
	}

	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	slug, err := worktree.ResolveDefaultSlug(workingDir(opts), daemonCfg)
	if err != nil {
		return err
	}
	path, err := worktree.ResolvePathFromSlugWithRegistry(slug, workingDir(opts), daemonCfg)
	if err != nil {
		return err
	}
	projectConfigPath := opts.ResolvedPaths.ProjectConfig
	if opts.ConfigPath == "" || opts.ConfigPath == config.DefaultProjectConfig {
		projectConfigPath = filepath.Join(path, config.DefaultProjectConfig)
	}
	projectCfg, _, err := config.LoadProjectConfig(projectConfigPath)
	if err != nil {
		return err
	}
	project, err := worktree.NormalizeIdentifierSegment(projectCfg.Project.Name)
	if err != nil {
		return fmt.Errorf("resolve project identifier: %w", err)
	}
	branch, err := worktree.ResolveBranch(path)
	if err != nil {
		return err
	}
	env := worktree.RuntimeEnvironment(projectCfg, project, slug, path, branch, proxyApexZone(daemonCfg))

	if output == "json" {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(env)
	}
	for _, key := range worktreeEnvironmentKeys {
		if _, err := fmt.Fprintf(out, "export %s=%s\n", key, shellQuote(env[key])); err != nil {
			return err
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
