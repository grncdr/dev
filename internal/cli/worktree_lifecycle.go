package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattn/go-shellwords"
	"github.com/spf13/cobra"

	"dev-mode/internal/config"
	"dev-mode/internal/worktree"
)

type worktreeCleanupOptions struct {
	DeleteBranch bool
	DryRun       bool
	Force        bool
}

func newWorktreeCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "manage worktree lifecycle",
	}
	cmd.AddCommand(newWorktreeAddCmd(opts))
	cmd.AddCommand(newWorktreeCleanupCmd(opts))
	cmd.AddCommand(newWorktreeListCmd(opts))
	return cmd
}

func newWorktreeAddCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <slug> [branch]",
		Short: "create a managed worktree",
		Long:  "Create a managed worktree. Accepts slug or project:slug format.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) == 2 {
				branch = strings.TrimSpace(args[1])
			}
			return runWorktreeAdd(opts, args[0], branch, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func newWorktreeCleanupCmd(opts *Options) *cobra.Command {
	cleanup := &worktreeCleanupOptions{}
	cmd := &cobra.Command{
		Use:   "cleanup [slug]",
		Short: "cleanup a managed worktree",
		Long:  "Cleanup a managed worktree. Accepts slug or project:slug format.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) > 0 {
				target = args[0]
			}
			return runWorktreeCleanup(opts, target, cleanup, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&cleanup.DeleteBranch, "delete-branch", false, "delete associated branch")
	cmd.Flags().BoolVar(&cleanup.DryRun, "dry-run", false, "show planned cleanup actions without mutating")
	cmd.Flags().BoolVar(&cleanup.Force, "force", false, "force cleanup and branch deletion")
	return cmd
}

func newWorktreeListCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list managed worktrees",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeList(opts, cmd.OutOrStdout())
		},
	}
	return cmd
}

func runWorktreeAdd(opts *Options, targetArg, branchArg string, out, errOut io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	repoProject, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return fmt.Errorf("resolve project identifier from repository path: %w", err)
	}
	target, err := parseProjectSlugWithDefault(targetArg, repoProject)
	if err != nil {
		return err
	}
	if target.Project != repoProject {
		return fmt.Errorf("project mismatch: target %q does not match current repository %q", target.Project, repoProject)
	}

	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	worktreeDir, err := config.ResolveWorktreeDir(daemonCfg)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(worktreeDir, target.Project, target.Slug)
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("worktree already exists at %s", targetPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check worktree path: %w", err)
	}

	// Check for DNS label conflicts (last path segment must be unique)
	newLabel := worktree.SlugDNSLabel(target.Slug)
	projectWorktreeDir := filepath.Join(worktreeDir, target.Project)
	if dirEntries, err := os.ReadDir(projectWorktreeDir); err == nil {
		for _, entry := range dirEntries {
			if entry.IsDir() && entry.Name() == newLabel && entry.Name() != target.Slug {
				return fmt.Errorf("slug %q conflicts with existing worktree (both use DNS label %q)",
					target.Slug, newLabel)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create worktree parent directory: %w", err)
	}

	cfg, err := loadProjectConfigFromDir(mainPath)
	if err != nil {
		return err
	}

	branchName := strings.TrimSpace(branchArg)
	if branchName == "" {
		branchName = target.Slug
	}
	branchExists, err := gitBranchExists(mainPath, branchName)
	if err != nil {
		return err
	}
	if branchArg == "" && branchExists {
		fmt.Fprintf(errOut, "warning: branch %q already exists; reusing existing branch\n", branchName)
	}

	hookEnv := lifecycleHookEnv(target.Project, target.Slug, targetPath, branchName, "add", false, proxyApexZone(daemonCfg))
	if err := runLifecycleHook(cfg.Hooks.PreWorktreeAdd, cfg.Commands.Wrapper, "pre_worktree_add", mainPath, hookEnv, out, errOut); err != nil {
		return err
	}

	var addErr error
	if branchExists {
		addErr = runGit(mainPath, "worktree", "add", targetPath, branchName)
	} else {
		addErr = runGit(mainPath, "worktree", "add", "-b", branchName, targetPath, "HEAD")
	}
	if addErr != nil {
		return addErr
	}

	if err := runLifecycleHook(cfg.Hooks.PostWorktreeAdd, cfg.Commands.Wrapper, "post_worktree_add", targetPath, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree created at %s, but %w", targetPath, err)
	}

	fmt.Fprintf(out, "created %s (%s)\n", targetPath, branchName)
	return nil
}

func runWorktreeCleanup(opts *Options, targetArg string, cleanup *worktreeCleanupOptions, out, errOut io.Writer) error {
	if cleanup == nil {
		cleanup = &worktreeCleanupOptions{}
	}
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	worktreeDir, err := config.ResolveWorktreeDir(daemonCfg)
	if err != nil {
		return err
	}

	resolved, err := resolveCleanupTarget(targetArg, worktreeDir)
	if err != nil {
		return err
	}
	if !resolved.exists {
		return fmt.Errorf("worktree not found at %s", resolved.path)
	}
	if samePath(resolved.path, resolved.mainPath) {
		return errors.New("cannot cleanup the main worktree")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if containsPath(cwd, resolved.path) && !cleanup.Force {
		return errors.New("cannot cleanup the current worktree while your shell is inside it (use --force)")
	}

	dirty, err := gitWorktreeDirty(resolved.path)
	if err != nil {
		return err
	}
	if dirty && !cleanup.Force {
		return errors.New("target worktree has uncommitted changes (use --force)")
	}

	cfg, err := loadProjectConfigFromDir(resolved.path)
	if err != nil {
		return err
	}
	hookEnv := lifecycleHookEnv(resolved.project, resolved.slug, resolved.path, resolved.branch, "cleanup", resolved.implicit, proxyApexZone(daemonCfg))

	if cleanup.DryRun {
		fmt.Fprintf(out, "would remove worktree: %s\n", resolved.path)
		if cleanup.DeleteBranch && resolved.branch != "" && resolved.branch != "HEAD" {
			fmt.Fprintf(out, "would delete branch: %s\n", resolved.branch)
		}
		return nil
	}

	if err := runLifecycleHook(cfg.Hooks.PreWorktreeCleanup, cfg.Commands.Wrapper, "pre_worktree_cleanup", resolved.path, hookEnv, out, errOut); err != nil {
		return err
	}

	removeArgs := []string{"worktree", "remove"}
	if cleanup.Force {
		removeArgs = append(removeArgs, "--force")
	}
	removeArgs = append(removeArgs, resolved.path)
	if err := runGit(resolved.mainPath, removeArgs...); err != nil {
		return err
	}

	if cleanup.DeleteBranch && resolved.branch != "" && resolved.branch != "HEAD" {
		branchArgs := []string{"branch", "-d", resolved.branch}
		if cleanup.Force {
			branchArgs = []string{"branch", "-D", resolved.branch}
		}
		if err := runGit(resolved.mainPath, branchArgs...); err != nil {
			return err
		}
	}

	if err := runLifecycleHook(cfg.Hooks.PostWorktreeCleanup, cfg.Commands.Wrapper, "post_worktree_cleanup", resolved.mainPath, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree cleaned up at %s, but %w", resolved.path, err)
	}

	fmt.Fprintf(out, "cleaned up %s\n", resolved.path)
	return nil
}

func runWorktreeList(opts *Options, out io.Writer) error {
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	worktreeDir, err := config.ResolveWorktreeDir(daemonCfg)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return fmt.Errorf("resolve project identifier from repository path: %w", err)
	}
	currentEntry, _ := matchCurrentEntry(entries, cwd)

	managedPrefix := filepath.Join(worktreeDir, project)
	type row struct {
		id     string
		path   string
		branch string
		flags  []string
	}
	rows := make([]row, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == "" {
			continue
		}
		isMain := samePath(entry.Path, mainPath)
		if !isMain && !containsPath(entry.Path, managedPrefix) {
			continue
		}
		slug, err := worktree.NormalizeIdentifierSegment(filepath.Base(entry.Path))
		if err != nil {
			continue
		}
		flags := []string{}
		if isMain {
			flags = append(flags, "main")
		}
		if currentEntry.Path != "" && samePath(entry.Path, currentEntry.Path) {
			flags = append(flags, "current")
		}
		dirty, err := gitWorktreeDirty(entry.Path)
		if err == nil && dirty {
			flags = append(flags, "dirty")
		}
		branch := worktree.BranchName(entry.Branch)
		if branch == "" {
			branch = "HEAD"
		}
		rows = append(rows, row{
			id:     project + ":" + slug,
			path:   entry.Path,
			branch: branch,
			flags:  flags,
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	if len(rows) == 0 {
		fmt.Fprintln(out, "no managed worktrees")
		return nil
	}
	for _, r := range rows {
		flagValue := "-"
		if len(r.flags) > 0 {
			flagValue = strings.Join(r.flags, ",")
		}
		fmt.Fprintf(out, "%s\tpath=%s\tbranch=%s\tflags=%s\n", r.id, r.path, r.branch, flagValue)
	}
	return nil
}

type cleanupTarget struct {
	project  string
	slug     string
	path     string
	mainPath string
	branch   string
	implicit bool
	exists   bool
}

func resolveCleanupTarget(arg, worktreeDir string) (*cleanupTarget, error) {
	if strings.TrimSpace(arg) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		entries, err := worktree.ListWorktreesInDir(cwd)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 || entries[0].Path == "" {
			return nil, errors.New("main worktree path missing")
		}
		current, err := matchCurrentEntry(entries, cwd)
		if err != nil {
			return nil, fmt.Errorf("unable to resolve current worktree: %w", err)
		}
		project, err := resolveProjectIdentifierFromMainPath(entries[0].Path)
		if err != nil {
			return nil, err
		}
		slug, err := worktree.NormalizeIdentifierSegment(filepath.Base(current.Path))
		if err != nil {
			return nil, err
		}
		return &cleanupTarget{
			project:  project,
			slug:     slug,
			path:     current.Path,
			mainPath: entries[0].Path,
			branch:   worktree.BranchName(current.Branch),
			implicit: true,
			exists:   true,
		}, nil
	}

	project, err := resolveProjectFromCurrentDir()
	if err != nil {
		return nil, err
	}
	parsed, err := parseProjectSlugWithDefault(arg, project)
	if err != nil {
		return nil, err
	}
	targetPath := filepath.Join(worktreeDir, parsed.Project, parsed.Slug)
	if _, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cleanupTarget{project: parsed.Project, slug: parsed.Slug, path: targetPath, implicit: false, exists: false}, nil
		}
		return nil, fmt.Errorf("check target worktree path: %w", err)
	}
	entries, err := worktree.ListWorktreesInDir(targetPath)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return nil, errors.New("main worktree path missing")
	}
	entry := findEntryByPath(entries, targetPath)
	if entry.Path == "" {
		return nil, fmt.Errorf("worktree path %s is not registered in git metadata", targetPath)
	}
	return &cleanupTarget{
		project:  parsed.Project,
		slug:     parsed.Slug,
		path:     targetPath,
		mainPath: entries[0].Path,
		branch:   worktree.BranchName(entry.Branch),
		implicit: false,
		exists:   true,
	}, nil
}

func parseProjectSlugWithDefault(arg, defaultProject string) (worktree.ProjectSlug, error) {
	trimmed := strings.TrimSpace(arg)
	if strings.Contains(trimmed, ":") {
		return worktree.ParseProjectSlug(trimmed)
	}
	return worktree.ParseProjectSlug(defaultProject + ":" + trimmed)
}

func resolveProjectFromCurrentDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return "", errors.New("main worktree path missing")
	}
	return resolveProjectIdentifierFromMainPath(entries[0].Path)
}

func resolveProjectIdentifierFromMainPath(mainPath string) (string, error) {
	cfg, err := loadProjectConfigFromDir(mainPath)
	if err != nil {
		return "", err
	}
	return worktree.NormalizeIdentifierSegment(cfg.Project.Name)
}

func loadDaemonConfig(opts *Options) (*config.DaemonConfig, error) {
	if opts == nil {
		return nil, errors.New("missing options")
	}
	if opts.ResolvedPaths.DaemonConfig == "" {
		return nil, errors.New("missing daemon config path")
	}
	cfg, _, err := config.LoadDaemonConfig(opts.ResolvedPaths.DaemonConfig)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadProjectConfigFromDir(dir string) (*config.ProjectConfig, error) {
	cfgPath := filepath.Join(dir, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func gitBranchExists(dir, branch string) (bool, error) {
	cmd := exec.Command("git", "-C", dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check branch existence: %w", err)
}

func gitWorktreeDirty(path string) (bool, error) {
	output, err := gitOutput(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func lifecycleHookEnv(project, slug, worktreePath, branch, operation string, implicit bool, apexZone string) map[string]string {
	zone := strings.TrimPrefix(strings.TrimSpace(apexZone), ".")
	if zone == "" {
		zone = "localhost"
	}
	localDNSName := worktree.SlugDNSLabel(slug) + "." + zone
	return map[string]string{
		"DEV_MODE_PROJECT":           project,
		"DEV_MODE_WORKTREE_SLUG":     slug,
		"DEV_MODE_WORKTREE_PATH":     worktreePath,
		"DEV_MODE_WORKTREE_BRANCH":   branch,
		"DEV_MODE_WORKTREE_DNS_NAME": localDNSName,
		"DEV_MODE_OPERATION":         operation,
		"DEV_MODE_IMPLICIT_TARGET":   fmt.Sprintf("%t", implicit),
	}
}

func runLifecycleHook(command, wrapper, phase, dir string, env map[string]string, out, errOut io.Writer) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	args, err := shellwords.Parse(command)
	if err != nil {
		return fmt.Errorf("%s hook parse failed: %w", phase, err)
	}
	if len(args) == 0 {
		return nil
	}
	if strings.TrimSpace(wrapper) != "" {
		args, err = applyHookWrapper(wrapper, args)
		if err != nil {
			return fmt.Errorf("%s hook wrapper parse failed: %w", phase, err)
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	hookEnv := cloneEnv(env)
	hookEnv["DEV_MODE_HOOK_NAME"] = phase
	cmd.Env = append(os.Environ(), formatEnv(hookEnv)...)
	cmd.Stdout = &prefixedLineWriter{prefix: "[hook " + phase + "] ", writer: out}
	cmd.Stderr = &prefixedLineWriter{prefix: "[hook " + phase + "] ", writer: errOut}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s hook failed: %w", phase, err)
	}
	return nil
}

func applyHookWrapper(wrapper string, command []string) ([]string, error) {
	wrapperArgs, err := shellwords.Parse(wrapper)
	if err != nil {
		return nil, err
	}
	if len(wrapperArgs) == 0 {
		return command, nil
	}
	out := make([]string, 0, len(wrapperArgs)+len(command))
	replaced := false
	for _, arg := range wrapperArgs {
		if strings.Contains(arg, "$COMMAND") {
			replaced = true
			parts := strings.Split(arg, "$COMMAND")
			if len(parts) == 2 {
				if parts[0] != "" {
					out = append(out, parts[0])
				}
				out = append(out, command...)
				if parts[1] != "" {
					out = append(out, parts[1])
				}
			} else {
				out = append(out, command...)
			}
			continue
		}
		out = append(out, arg)
	}
	if !replaced {
		out = append(out, command...)
	}
	return out, nil
}

func formatEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%s", key, values[key]))
	}
	return out
}

func cloneEnv(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

type prefixedLineWriter struct {
	prefix string
	writer io.Writer
	buffer bytes.Buffer
}

func (w *prefixedLineWriter) Write(p []byte) (int, error) {
	if w.writer == nil {
		return len(p), nil
	}
	total := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx == -1 {
			_, _ = w.buffer.Write(p)
			break
		}
		_, _ = w.buffer.Write(p[:idx])
		if _, err := fmt.Fprintf(w.writer, "%s%s\n", w.prefix, w.buffer.String()); err != nil {
			return 0, err
		}
		w.buffer.Reset()
		p = p[idx+1:]
	}
	return total, nil
}

func matchCurrentEntry(entries []worktree.Entry, cwd string) (worktree.Entry, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return worktree.Entry{}, err
		}
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return worktree.Entry{}, err
	}
	var best worktree.Entry
	bestLen := -1
	for _, entry := range entries {
		if entry.Path == "" {
			continue
		}
		pathAbs, err := filepath.Abs(entry.Path)
		if err != nil {
			continue
		}
		if containsPath(cwdAbs, pathAbs) && len(pathAbs) > bestLen {
			best = entry
			bestLen = len(pathAbs)
		}
	}
	if bestLen < 0 {
		return worktree.Entry{}, errors.New("current directory is not a git worktree")
	}
	return best, nil
}

func findEntryByPath(entries []worktree.Entry, path string) worktree.Entry {
	for _, entry := range entries {
		if samePath(entry.Path, path) {
			return entry
		}
	}
	return worktree.Entry{}
}

func containsPath(path, prefix string) bool {
	pathAbs, err := canonicalPath(path)
	if err != nil {
		pathAbs = filepath.Clean(path)
	}
	prefixAbs, err := canonicalPath(prefix)
	if err != nil {
		prefixAbs = filepath.Clean(prefix)
	}
	if pathAbs == prefixAbs {
		return true
	}
	return strings.HasPrefix(pathAbs, prefixAbs+string(filepath.Separator))
}
