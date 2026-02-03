package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var validInitProjectPattern = regexp.MustCompile(`^[a-z0-9/_-]+$`)

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

	projectName := filepath.Base(cwd)
	if inferred, ok := inferProjectNameFromRemote(cwd, "origin"); ok {
		projectName = inferred
	}

	template := buildInitTemplate(projectName)
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
		"# pre_worktree_add = \"bin/pre-worktree-add\"",
		"# post_worktree_add = \"bin/post-worktree-add\"",
		"# pre_worktree_cleanup = \"bin/pre-worktree-cleanup\"",
		"# post_worktree_cleanup = \"bin/post-worktree-cleanup\"",
		"# pre_start = \"bin/pre-start\"",
		"# post_start = \"bin/post-start\"",
		"# pre_stop = \"bin/pre-stop\"",
		"# post_stop = \"bin/post-stop\"",
		"",
	}
	return strings.Join(lines, "\n")
}

func inferProjectNameFromRemote(repoDir, remote string) (string, bool) {
	if strings.TrimSpace(repoDir) == "" || strings.TrimSpace(remote) == "" {
		return "", false
	}
	cmd := exec.Command("git", "-C", repoDir, "remote", "get-url", remote)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return parseOwnerRepoFromRemoteURL(strings.TrimSpace(string(out)))
}

func parseOwnerRepoFromRemoteURL(raw string) (string, bool) {
	remote := strings.TrimSpace(raw)
	if remote == "" {
		return "", false
	}

	path := ""
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", false
		}
		path = u.Path
	} else if strings.Contains(remote, ":") {
		parts := strings.SplitN(remote, ":", 2)
		path = parts[1]
	}
	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	if path == "" {
		return "", false
	}

	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return "", false
	}
	owner := strings.TrimSpace(segments[len(segments)-2])
	repo := strings.TrimSpace(segments[len(segments)-1])
	if owner == "" || repo == "" {
		return "", false
	}
	project := strings.ToLower(owner + "/" + repo)
	if !validInitProjectPattern.MatchString(project) {
		return "", false
	}
	return project, true
}
