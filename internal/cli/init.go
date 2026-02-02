package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newInitCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "initialize project config",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(opts, os.Stdout)
		},
	}
	return cmd
}

func runInit(opts *Options, out io.Writer) error {
	if opts == nil {
		return fmt.Errorf("missing options")
	}
	if opts.ResolvedPaths.ProjectConfig == "" {
		return fmt.Errorf("missing resolved config path")
	}

	if _, err := os.Stat(opts.ResolvedPaths.ProjectConfig); err == nil {
		return fmt.Errorf("config already exists: %s", opts.ResolvedPaths.ProjectConfig)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check config path: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	template := buildInitTemplate(filepath.Base(cwd))
	if err := os.WriteFile(opts.ResolvedPaths.ProjectConfig, []byte(template), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(out, "Initialized %s\n", opts.ResolvedPaths.ProjectConfig)
	return nil
}

func buildInitTemplate(projectName string) string {
	lines := []string{
		"[project]",
		fmt.Sprintf("name = %q", projectName),
		"",
		"# [process.server]",
		"# command = \"my-server\"",
		"# port = \"random\"",
		"#",
		"# [[process.server.proxy]]",
		"# subdomain = null",
		"# path = \"/\"",
		"# match = \"prefix\"",
		"",
		"# [hooks]",
		"# post_create = \"bin/setup-worktree\"",
		"# pre_cleanup = \"bin/teardown-worktree\"",
		"# pre_start = \"bin/pre-start\"",
		"# post_start = \"bin/post-start\"",
		"# pre_stop = \"bin/pre-stop\"",
		"# post_stop = \"bin/post-stop\"",
		"",
	}
	return strings.Join(lines, "\n")
}
