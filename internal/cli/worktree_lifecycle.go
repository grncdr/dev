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

	"dev/internal/config"
	"dev/internal/procenv"
	"dev/internal/worktree"
)

type worktreeCleanupOptions struct {
	DeleteBranch bool
	DryRun       bool
	Force        bool
}

type worktreeListOptions struct {
	AllProjects bool
}

func newWorktreeCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "manage worktree lifecycle",
	}
	cmd.AddCommand(newWorktreeAddCmd(opts))
	cmd.AddCommand(newWorktreeRegisterCmd(opts))
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

func newWorktreeRegisterCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register [slug]",
		Short: "register current git worktree in dev state",
		Long:  "Register current git worktree so it can be managed outside daemon.worktree_dir. Accepts slug or project:slug format.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			return runWorktreeRegister(opts, target, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	return cmd
}

func newWorktreeListCmd(opts *Options) *cobra.Command {
	list := &worktreeListOptions{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list managed worktrees",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeList(opts, list, cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&list.AllProjects, "all-projects", false, "list managed worktrees across all projects")
	return cmd
}

func runWorktreeRegister(opts *Options, targetArg string, out, errOut io.Writer) error {
	cwd := workingDir(opts)
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	current, err := matchCurrentEntry(entries, cwd)
	if err != nil {
		return fmt.Errorf("unable to resolve current worktree: %w", err)
	}
	if samePath(current.Path, mainPath) {
		return errors.New("cannot register the main worktree")
	}

	cfg, err := loadProjectConfigFromDir(mainPath)
	if err != nil {
		return err
	}
	projectID, err := worktree.NormalizeIdentifierSegment(cfg.Project.Name)
	if err != nil {
		return err
	}

	var target worktree.Identifier
	if strings.TrimSpace(targetArg) == "" {
		slug, err := worktree.NormalizeIdentifierSegment(filepath.Base(current.Path))
		if err != nil {
			return fmt.Errorf("resolve slug from current worktree path: %w", err)
		}
		target = worktree.Identifier{Project: projectID, Slug: slug}
	} else {
		target, err = parseIdentifierWithDefault(targetArg, projectID)
		if err != nil {
			return err
		}
		if target.Project != projectID {
			return fmt.Errorf("project mismatch: target %q does not match current repository %q", target.Project, projectID)
		}
	}

	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	daemonHooks := config.ResolveDaemonWorktreeLifecycleHooks(daemonCfg, cfg.Project.Name, target.Project)
	branch := worktree.BranchName(current.Branch)
	hookEnv := lifecycleHookEnv(target.Project, target.Slug, current.Path, branch, "add", false, mainPath, proxyApexZone(daemonCfg))
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Identifier: target,
		Path:       current.Path,
		MainPath:   mainPath,
		Branch:     branch,
	}); err != nil {
		return err
	}
	if err := runLifecycleHook(cfg.Hooks.PostWorktreeAdd, cfg.Commands.Wrapper, "post_worktree_add", current.Path, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree registered at %s, but %w", current.Path, err)
	}
	if err := runLifecycleHooks(daemonHooks.PostWorktreeAdd, "", "post_worktree_add", current.Path, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree registered at %s, but %w", current.Path, err)
	}

	fmt.Fprintf(out, "registered %s (%s:%s)\n", current.Path, target.Project, target.Slug)
	return nil
}

func runWorktreeAdd(opts *Options, targetArg, branchArg string, out, errOut io.Writer) error {
	cwd := workingDir(opts)
	var err error
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
	target, err := parseIdentifierWithDefault(targetArg, repoProject)
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
	daemonHooks := config.ResolveDaemonWorktreeLifecycleHooks(daemonCfg, cfg.Project.Name, target.Project)

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

	hookEnv := lifecycleHookEnv(target.Project, target.Slug, targetPath, branchName, "add", false, mainPath, proxyApexZone(daemonCfg))
	if err := runLifecycleHook(cfg.Hooks.PreWorktreeAdd, cfg.Commands.Wrapper, "pre_worktree_add", mainPath, hookEnv, out, errOut); err != nil {
		return err
	}
	if err := runLifecycleHooks(daemonHooks.PreWorktreeAdd, "", "pre_worktree_add", mainPath, hookEnv, out, errOut); err != nil {
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

	if err := worktree.Register(daemonCfg, worktree.Registration{
		Identifier: target,
		Path:       targetPath,
		MainPath:   mainPath,
		Branch:     branchName,
	}); err != nil {
		return fmt.Errorf("worktree created at %s, but %w", targetPath, err)
	}

	if err := runLifecycleHook(cfg.Hooks.PostWorktreeAdd, cfg.Commands.Wrapper, "post_worktree_add", targetPath, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree created at %s, but %w", targetPath, err)
	}
	if err := runLifecycleHooks(daemonHooks.PostWorktreeAdd, "", "post_worktree_add", targetPath, hookEnv, out, errOut); err != nil {
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

	cwd := workingDir(opts)
	resolved, err := resolveCleanupTarget(targetArg, daemonCfg, cwd, opts)
	if err != nil {
		return err
	}
	postCleanupCfg, err := loadProjectConfigFromDir(resolved.mainPath)
	if err != nil {
		return err
	}
	daemonHooks := config.ResolveDaemonWorktreeLifecycleHooks(daemonCfg, postCleanupCfg.Project.Name, resolved.project)
	hookEnv := lifecycleHookEnv(resolved.project, resolved.slug, resolved.path, resolved.branch, "cleanup", resolved.implicit, resolved.mainPath, proxyApexZone(daemonCfg))
	if !resolved.exists {
		fmt.Fprintf(errOut, "warning: worktree not found at %s; cleaning up daemon state only\n", resolved.path)
		if cleanup.DryRun {
			fmt.Fprintf(out, "would remove stale worktree registration: %s:%s\n", resolved.project, resolved.slug)
			return nil
		}
		if err := worktree.Unregister(daemonCfg, resolved.project, resolved.slug); err != nil {
			return fmt.Errorf("cleanup stale worktree registration: %w", err)
		}
		if err := runLifecycleHooks(daemonHooks.PostWorktreeCleanup, "", "post_worktree_cleanup", resolved.mainPath, hookEnv, out, errOut); err != nil {
			return fmt.Errorf("worktree daemon state cleaned up at %s, but %w", resolved.path, err)
		}
		if err := runLifecycleHook(postCleanupCfg.Hooks.PostWorktreeCleanup, postCleanupCfg.Commands.Wrapper, "post_worktree_cleanup", resolved.mainPath, hookEnv, out, errOut); err != nil {
			return fmt.Errorf("worktree daemon state cleaned up at %s, but %w", resolved.path, err)
		}
		fmt.Fprintf(out, "cleaned up daemon state for missing worktree %s:%s\n", resolved.project, resolved.slug)
		return nil
	}
	if samePath(resolved.path, resolved.mainPath) {
		return errors.New("cannot cleanup the main worktree")
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

	preCleanupCfg, err := loadProjectConfigFromDir(resolved.path)
	if err != nil {
		return err
	}

	if cleanup.DryRun {
		fmt.Fprintf(out, "would remove worktree: %s\n", resolved.path)
		if cleanup.DeleteBranch && resolved.branch != "" && resolved.branch != "HEAD" {
			fmt.Fprintf(out, "would delete branch: %s\n", resolved.branch)
		}
		return nil
	}

	if err := runLifecycleHooks(daemonHooks.PreWorktreeCleanup, "", "pre_worktree_cleanup", resolved.path, hookEnv, out, errOut); err != nil {
		return err
	}
	if err := runLifecycleHook(preCleanupCfg.Hooks.PreWorktreeCleanup, preCleanupCfg.Commands.Wrapper, "pre_worktree_cleanup", resolved.path, hookEnv, out, errOut); err != nil {
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

	if err := worktree.Unregister(daemonCfg, resolved.project, resolved.slug); err != nil {
		return fmt.Errorf("worktree cleaned up at %s, but %w", resolved.path, err)
	}

	if err := runLifecycleHooks(daemonHooks.PostWorktreeCleanup, "", "post_worktree_cleanup", resolved.mainPath, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree cleaned up at %s, but %w", resolved.path, err)
	}
	if err := runLifecycleHook(postCleanupCfg.Hooks.PostWorktreeCleanup, postCleanupCfg.Commands.Wrapper, "post_worktree_cleanup", resolved.mainPath, hookEnv, out, errOut); err != nil {
		return fmt.Errorf("worktree cleaned up at %s, but %w", resolved.path, err)
	}

	fmt.Fprintf(out, "cleaned up %s\n", resolved.path)
	return nil
}

type worktreeListRow struct {
	id     string
	path   string
	branch string
	flags  []string
}

func runWorktreeList(opts *Options, list *worktreeListOptions, out io.Writer) error {
	if list == nil {
		list = &worktreeListOptions{}
	}
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	cwd := workingDir(opts)
	rows := []worktreeListRow{}
	if list.AllProjects {
		rows, err = collectAllProjectWorktreeRows(daemonCfg, cwd)
	} else {
		rows, err = collectCurrentProjectWorktreeRows(daemonCfg, cwd)
	}
	if err != nil {
		return err
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
		fmt.Fprintf(out, "%s\tpath=%s\tbranch=%s\tflags=%s\n", r.id, config.CollapseUserPath(r.path), r.branch, flagValue)
	}
	return nil
}

func collectCurrentProjectWorktreeRows(daemonCfg *config.DaemonConfig, cwd string) ([]worktreeListRow, error) {
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return nil, errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return nil, fmt.Errorf("resolve project identifier from repository path: %w", err)
	}
	registered, err := worktree.ListRegisteredWorktrees(daemonCfg, project)
	if err != nil {
		return nil, err
	}
	return collectProjectWorktreeRows(project, mainPath, entries, registered, cwd, false)
}

func collectAllProjectWorktreeRows(daemonCfg *config.DaemonConfig, cwd string) ([]worktreeListRow, error) {
	rows := []worktreeListRow{}
	includedProjects := map[string]struct{}{}

	if currentRows, project, err := tryCollectCurrentProjectWorktreeRows(daemonCfg, cwd); err == nil {
		rows = append(rows, currentRows...)
		includedProjects[project] = struct{}{}
	}

	registered, err := worktree.ListRegisteredWorktrees(daemonCfg, "")
	if err != nil {
		return nil, err
	}
	registeredByProject := map[string][]worktree.Registration{}
	for _, entry := range registered {
		registeredByProject[entry.Project] = append(registeredByProject[entry.Project], entry)
	}
	for project, entries := range registeredByProject {
		if _, ok := includedProjects[project]; ok {
			continue
		}
		mainPath := projectMainPath(entries)
		gitEntries := []worktree.Entry(nil)
		if strings.TrimSpace(mainPath) != "" {
			if listed, err := worktree.ListWorktreesInDir(mainPath); err == nil {
				gitEntries = listed
			}
		}
		projectRows, err := collectProjectWorktreeRows(project, mainPath, gitEntries, entries, cwd, true)
		if err != nil {
			return nil, err
		}
		rows = append(rows, projectRows...)
	}
	return rows, nil
}

func tryCollectCurrentProjectWorktreeRows(daemonCfg *config.DaemonConfig, cwd string) ([]worktreeListRow, string, error) {
	entries, err := worktree.ListWorktreesInDir(cwd)
	if err != nil {
		return nil, "", err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return nil, "", errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolve project identifier from repository path: %w", err)
	}
	registered, err := worktree.ListRegisteredWorktrees(daemonCfg, project)
	if err != nil {
		return nil, "", err
	}
	rows, err := collectProjectWorktreeRows(project, mainPath, entries, registered, cwd, false)
	if err != nil {
		return nil, "", err
	}
	return rows, project, nil
}

func collectProjectWorktreeRows(project, mainPath string, gitEntries []worktree.Entry, registered []worktree.Registration, cwd string, allowMissingMain bool) ([]worktreeListRow, error) {
	rows := []worktreeListRow{}
	if strings.TrimSpace(mainPath) == "" && len(gitEntries) > 0 {
		mainPath = gitEntries[0].Path
	}
	currentEntry, _ := matchCurrentEntry(gitEntries, cwd)
	registeredByID := map[string]worktree.Registration{}
	for _, entry := range registered {
		registeredByID[entry.Project+":"+entry.Slug] = entry
	}
	if allowMissingMain && strings.TrimSpace(mainPath) != "" {
		if _, ok := findGitEntryByPath(gitEntries, mainPath); !ok {
			slug, err := worktreeListMainSlug(mainPath)
			if err != nil {
				return nil, err
			}
			rows = append(rows, worktreeListRow{
				id:     project + ":" + slug,
				path:   mainPath,
				branch: "HEAD",
				flags:  []string{"main", "missing"},
			})
		}
	}
	for _, entry := range gitEntries {
		if entry.Path == "" {
			continue
		}
		isMain := strings.TrimSpace(mainPath) != "" && samePath(entry.Path, mainPath)
		var registeredEntry worktree.Registration
		if !isMain {
			matched, ok := findRegisteredEntryByPath(registered, entry.Path)
			if !ok {
				continue
			}
			registeredEntry = matched
		}
		slug := registeredEntry.Slug
		if isMain {
			var err error
			slug, err = worktreeListMainSlug(mainPath)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(slug) == "" {
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
		rows = append(rows, worktreeListRow{
			id:     project + ":" + slug,
			path:   entry.Path,
			branch: branch,
			flags:  flags,
		})
	}
	for id, entry := range registeredByID {
		if _, ok := findGitEntryByPath(gitEntries, entry.Path); ok {
			continue
		}
		branch := strings.TrimSpace(entry.Branch)
		if branch == "" {
			branch = "HEAD"
		}
		rows = append(rows, worktreeListRow{
			id:     id,
			path:   entry.Path,
			branch: branch,
			flags:  []string{"missing"},
		})
	}
	return rows, nil
}

func projectMainPath(entries []worktree.Registration) string {
	for _, entry := range entries {
		if strings.TrimSpace(entry.MainPath) != "" {
			return entry.MainPath
		}
	}
	return ""
}

func worktreeListMainSlug(mainPath string) (string, error) {
	if configuredMainSlug, ok, err := worktree.ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if ok {
		return configuredMainSlug, nil
	}
	return "main", nil
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

func resolveCleanupTarget(arg string, daemonCfg *config.DaemonConfig, cwd string, opts *Options) (*cleanupTarget, error) {
	if strings.TrimSpace(arg) == "" {
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
		slug := ""
		if registered, ok, err := worktree.FindRegistrationByPath(daemonCfg, project, current.Path); err != nil {
			return nil, err
		} else if ok {
			slug = registered.Slug
		} else {
			if samePath(current.Path, entries[0].Path) {
				slug = "main"
			} else {
				return nil, errors.New("current worktree is not registered (run `dev worktree register` in that worktree)")
			}
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

	project, err := resolveProjectFromCurrentDir(opts)
	if err != nil {
		return nil, err
	}
	parsed, err := parseIdentifierWithDefault(arg, project)
	if err != nil {
		return nil, err
	}
	registered, ok, err := worktree.FindRegisteredWorktree(daemonCfg, parsed.Project, parsed.Slug)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("worktree %s:%s is not registered (run `dev worktree register` from that worktree)", parsed.Project, parsed.Slug)
	}
	targetPath := registered.Path
	if _, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			mainPath := strings.TrimSpace(registered.MainPath)
			if mainPath == "" {
				mainPath, err = worktree.ResolvePathFromProjectSlug(parsed.Project, "main", daemonCfg)
				if err != nil {
					return nil, err
				}
			}
			return &cleanupTarget{
				project:  parsed.Project,
				slug:     parsed.Slug,
				path:     targetPath,
				mainPath: mainPath,
				branch:   strings.TrimSpace(registered.Branch),
				implicit: false,
				exists:   false,
			}, nil
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

func parseIdentifierWithDefault(arg, defaultProject string) (worktree.Identifier, error) {
	trimmed := strings.TrimSpace(arg)
	if strings.Contains(trimmed, ":") {
		return worktree.ParseIdentifier(trimmed)
	}
	return worktree.ParseIdentifier(defaultProject + ":" + trimmed)
}

func resolveProjectFromCurrentDir(opts *Options) (string, error) {
	cwd := workingDir(opts)
	var err error
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

func lifecycleHookEnv(project, slug, worktreePath, branch, operation string, implicit bool, mainWorktreePath, apexZone string) map[string]string {
	zone := strings.TrimPrefix(strings.TrimSpace(apexZone), ".")
	if zone == "" {
		zone = "localhost"
	}
	localDNSName := worktree.SlugDNSLabel(slug) + "." + zone
	return map[string]string{
		"DEV_PROJECT":           project,
		"DEV_WORKTREE_SLUG":     slug,
		"DEV_WORKTREE_PATH":     worktreePath,
		"DEV_WORKTREE_BRANCH":   branch,
		"DEV_MAIN_WORKTREE":     mainWorktreePath,
		"DEV_WORKTREE_DNS_NAME": localDNSName,
		"DEV_OPERATION":         operation,
		"DEV_IMPLICIT_TARGET":   fmt.Sprintf("%t", implicit),
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
	args, err = config.ExpandCommandPath(args)
	if err != nil {
		return fmt.Errorf("%s hook path expansion failed: %w", phase, err)
	}
	if strings.TrimSpace(wrapper) != "" {
		args, err = procenv.ApplyWrapper(wrapper, args)
		if err != nil {
			return fmt.Errorf("%s hook wrapper parse failed: %w", phase, err)
		}
		args, err = config.ExpandCommandPath(args)
		if err != nil {
			return fmt.Errorf("%s hook wrapper path expansion failed: %w", phase, err)
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	hookEnv := procenv.CloneEnv(env)
	hookEnv["DEV_HOOK_NAME"] = phase
	cmd.Env = append(os.Environ(), procenv.FormatEnv(hookEnv)...)
	cmd.Stdout = &prefixedLineWriter{prefix: "[hook " + phase + "] ", writer: out}
	cmd.Stderr = &prefixedLineWriter{prefix: "[hook " + phase + "] ", writer: errOut}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s hook failed: %w", phase, err)
	}
	return nil
}

func runLifecycleHooks(commands []string, wrapper, phase, dir string, env map[string]string, out, errOut io.Writer) error {
	for _, command := range commands {
		if err := runLifecycleHook(command, wrapper, phase, dir, env, out, errOut); err != nil {
			return err
		}
	}
	return nil
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
	if strings.TrimSpace(cwd) == "" {
		return worktree.Entry{}, errors.New("cwd is required")
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

func findGitEntryByPath(entries []worktree.Entry, path string) (worktree.Entry, bool) {
	entry := findEntryByPath(entries, path)
	if entry.Path == "" {
		return worktree.Entry{}, false
	}
	return entry, true
}

func findRegisteredEntryByPath(entries []worktree.Registration, path string) (worktree.Registration, bool) {
	for _, entry := range entries {
		if samePath(entry.Path, path) {
			return entry, true
		}
	}
	return worktree.Registration{}, false
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
