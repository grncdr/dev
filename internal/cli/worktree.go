package cli

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/daemon"
	"dev/internal/worktree"
)

func newWorktreeStartCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [process_identifier...]",
		Short: "start processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeStart(args, opts)
		},
	}
	return cmd
}

func newWorktreeStopCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop [process_identifier...]",
		Short: "stop processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeStop(args, opts)
		},
	}
	return cmd
}

func newWorktreeRestartCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart [process_identifier...]",
		Short: "restart processes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeRestart(args, opts)
		},
	}
	return cmd
}

func newWorktreeStatusCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [process_identifier...]",
		Short: "show process status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorktreeStatus(args, opts)
		},
	}
	cmd.Aliases = []string{"status"}
	return cmd
}

func runWorktreeStart(targetArgs []string, opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	targets, err := resolveProcessTargets(targetArgs, opts)
	if err != nil {
		return err
	}
	for i, slug := range sortedTargetSlugs(targets) {
		target := targets[slug]
		resp, err := client.ProcessStart(ctx, slug, target.processList(), target.all)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Println()
		}
		printWorktreeStatus(resp)
	}
	return nil
}

func runWorktreeStop(targetArgs []string, opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	targets, err := resolveProcessTargets(targetArgs, opts)
	if err != nil {
		return err
	}
	for i, slug := range sortedTargetSlugs(targets) {
		target := targets[slug]
		resp, err := client.ProcessStop(ctx, slug, target.processList(), target.all)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Println()
		}
		printWorktreeStatus(resp)
	}
	return nil
}

func runWorktreeStatus(targetArgs []string, opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}

	targets, err := resolveProcessTargets(targetArgs, opts)
	if err != nil {
		return err
	}

	client := daemon.NewClient(socketPath)
	daemonUp, err := daemonRunning(socketPath)
	if err != nil {
		return err
	}
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	mainStatusByWorktree := map[string]*daemon.WorktreeStatus{}
	cwd := workingDir(opts)

	for i, slug := range sortedTargetSlugs(targets) {
		projectPath, _ := worktree.ResolvePathFromSlugWithRegistry(slug, cwd, daemonCfg)
		var cfg *config.ProjectConfig
		if projectPath != "" {
			cfgPath := filepath.Join(projectPath, config.DefaultProjectConfig)
			if loaded, _, err := config.LoadProjectConfig(cfgPath); err == nil {
				cfg = loaded
			}
		}
		if i > 0 {
			fmt.Println()
		}
		var status *daemon.WorktreeStatus
		if daemonUp {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			resp, err := client.WorktreeStatus(ctx, slug)
			cancel()
			if err == nil {
				status = filterWorktreeStatus(resp, targets[slug])
			}
		}
		mainStatus := status
		mainSlug := mainWorktreeSlug(cfg)
		if daemonUp && shouldUseMainWorktreeStatus(cfg, projectPath) {
			cacheKey := projectPath + "\x00" + mainSlug
			if cached, ok := mainStatusByWorktree[cacheKey]; ok {
				mainStatus = cached
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				resp, err := client.WorktreeStatus(ctx, mainSlug)
				cancel()
				if err == nil {
					mainStatus = resp
				}
				mainStatusByWorktree[cacheKey] = mainStatus
			}
		}
		printWorktreeDetailedStatus(slug, projectPath, cfg, status, mainStatus, daemonUp, targets[slug])
	}
	return nil
}

func runWorktreeRestart(targetArgs []string, opts *Options) error {
	if err := runWorktreeStop(targetArgs, opts); err != nil {
		return err
	}
	return runWorktreeStart(targetArgs, opts)
}

func printWorktreeStatus(status *daemon.WorktreeStatus) {
	if status == nil {
		return
	}
	fmt.Printf("worktree %s\n", status.Slug)
	if len(status.Processes) == 0 {
		fmt.Println("  (no processes)")
		return
	}
	for _, proc := range status.Processes {
		if proc.PID > 0 {
			fmt.Printf("  %s: %s (pid %d)\n", proc.Name, proc.Status, proc.PID)
		} else {
			fmt.Printf("  %s: %s\n", proc.Name, proc.Status)
		}
	}
}

func filterWorktreeStatus(status *daemon.WorktreeStatus, target *processTarget) *daemon.WorktreeStatus {
	if status == nil || target == nil || target.all || len(target.processes) == 0 {
		return status
	}
	filtered := make([]daemon.ProcessStatus, 0, len(status.Processes))
	for _, proc := range status.Processes {
		if target.processes[proc.Name] {
			filtered = append(filtered, proc)
		}
	}
	filteredRouting := daemon.WorktreeRouting{
		Local:         filterRoutesByTarget(status.Routing.Local, target),
		Gateway:       filterRoutesByTarget(status.Routing.Gateway, target),
		GatewayURL:    status.Routing.GatewayURL,
		GatewayStatus: status.Routing.GatewayStatus,
	}
	return &daemon.WorktreeStatus{Slug: status.Slug, Processes: filtered, Routing: filteredRouting}
}

func samePath(a, b string) bool {
	aa, err := canonicalPath(a)
	if err != nil {
		return a == b
	}
	bb, err := canonicalPath(b)
	if err != nil {
		return a == b
	}
	return aa == bb
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil
	}
	return resolved, nil
}

func printWorktreeDetailedStatus(slug, projectPath string, cfg *config.ProjectConfig, status, mainStatus *daemon.WorktreeStatus, daemonUp bool, target *processTarget) {
	projectName := "(unknown)"
	if cfg != nil && strings.TrimSpace(cfg.Project.Name) != "" {
		projectName = cfg.Project.Name
	}
	fmt.Printf("Project: %s\n", projectName)
	worktreePath := projectPath
	if strings.TrimSpace(worktreePath) == "" {
		worktreePath = "(unknown)"
	}
	fmt.Printf("Worktree: %s (%s)\n", slug, worktreePath)

	proxyByProcess := map[string][]string{}
	if status != nil {
		proxyByProcess = status.Routing.Local
	}
	if target != nil && !target.all && len(target.processes) > 0 {
		proxyByProcess = filterRoutesByTarget(proxyByProcess, target)
	}
	fmt.Println("Proxy:")
	printRouteMappings(proxyByProcess)

	gatewayURL := ""
	connection := "disconnected"
	gatewayByProcess := map[string][]string{}
	if status != nil {
		gatewayURL = strings.TrimSpace(status.Routing.GatewayURL)
		if status.Routing.GatewayStatus != "" {
			connection = status.Routing.GatewayStatus
		}
		gatewayByProcess = status.Routing.Gateway
	}
	if gatewayURL != "" {
		fmt.Println()
		fmt.Printf("Gateway: %s [%s]\n", gatewayURL, connection)
		printRouteMappings(filterRoutesByTarget(gatewayByProcess, target))
	}

	fmt.Println()
	fmt.Println("Processes:")
	printProcesses(status, mainStatus, cfg, projectPath, daemonUp, target)
}

func filterRoutesByTarget(routes map[string][]string, target *processTarget) map[string][]string {
	if target == nil || target.all || len(target.processes) == 0 {
		return routes
	}
	filtered := map[string][]string{}
	for process, mappings := range routes {
		if targetIncludesProcess(target, process) {
			filtered[process] = mappings
		}
	}
	return filtered
}

func targetIncludesProcess(target *processTarget, process string) bool {
	if target == nil || target.all || len(target.processes) == 0 {
		return true
	}
	return target.processes[process]
}

func printRouteMappings(byProcess map[string][]string) {
	lines := routeMappingLines(byProcess)
	if len(lines) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
}

func routeMappingLines(byProcess map[string][]string) []string {
	type pathMapping struct {
		path    string
		process string
	}
	grouped := map[string][]pathMapping{}
	ungrouped := []string{}
	for process, routes := range byProcess {
		for _, route := range routes {
			parsed, err := url.Parse(route)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				ungrouped = append(ungrouped, fmt.Sprintf("%s -> %s", route, process))
				continue
			}
			switch parsed.Scheme {
			case "http", "https":
				hostKey := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
				path := parsed.EscapedPath()
				if path == "" {
					path = "/"
				}
				grouped[hostKey] = append(grouped[hostKey], pathMapping{
					path:    path,
					process: process,
				})
			default:
				ungrouped = append(ungrouped, fmt.Sprintf("%s -> %s", route, process))
			}
		}
	}
	sort.Strings(ungrouped)
	hosts := make([]string, 0, len(grouped))
	for host := range grouped {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	lines := make([]string, 0, len(ungrouped)+len(hosts)*2)
	lines = append(lines, ungrouped...)
	for _, host := range hosts {
		mappings := grouped[host]
		sort.Slice(mappings, func(i, j int) bool {
			if mappings[i].path == mappings[j].path {
				return mappings[i].process < mappings[j].process
			}
			return mappings[i].path < mappings[j].path
		})
		if len(mappings) == 1 && mappings[0].path == "/" {
			lines = append(lines, fmt.Sprintf("%s -> %s", host, mappings[0].process))
			continue
		}
		lines = append(lines, host)
		maxPathLen := 0
		for _, mapping := range mappings {
			if l := len(mapping.path); l > maxPathLen {
				maxPathLen = l
			}
		}
		for _, mapping := range mappings {
			lines = append(lines, fmt.Sprintf("  %-*s -> %s", maxPathLen, mapping.path, mapping.process))
		}
	}
	return lines
}

func printProcesses(status, mainStatus *daemon.WorktreeStatus, cfg *config.ProjectConfig, projectPath string, daemonUp bool, target *processTarget) {
	if !daemonUp {
		fmt.Println("  daemon not running")
		return
	}
	statusByName := map[string]daemon.ProcessStatus{}
	if status != nil {
		for _, proc := range status.Processes {
			statusByName[proc.Name] = proc
		}
	}
	mainStatusByName := map[string]daemon.ProcessStatus{}
	if mainStatus != nil {
		for _, proc := range mainStatus.Processes {
			mainStatusByName[proc.Name] = proc
		}
	}
	names := map[string]bool{}
	if cfg != nil {
		for name := range cfg.Processes {
			names[name] = true
		}
	}
	for name := range statusByName {
		names[name] = true
	}
	if target != nil && !target.all && len(target.processes) > 0 {
		for name := range names {
			if !targetIncludesProcess(target, name) {
				delete(names, name)
			}
		}
	}
	if len(names) == 0 {
		fmt.Println("  (none)")
		return
	}
	isMainWorktree := isMainWorktreePath(projectPath)
	mainSlug := mainWorktreeSlug(cfg)
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		proc, fromWorktree := resolveProcessStatus(name, statusByName, mainStatusByName, cfg, isMainWorktree, mainSlug)
		fmt.Printf("  %s\n", formatProcessLine(proc, fromWorktree))
	}
}

func resolveProcessStatus(name string, statusByName, mainStatusByName map[string]daemon.ProcessStatus, cfg *config.ProjectConfig, isMainWorktree bool, mainSlug string) (daemon.ProcessStatus, string) {
	proc, ok := statusByName[name]
	fromWorktree := ""
	if isSingletonProcess(cfg, name) && !isMainWorktree {
		fromWorktree = mainSlug
		if mainProc, ok := mainStatusByName[name]; ok {
			return mainProc, fromWorktree
		}
	}
	if !ok {
		proc = daemon.ProcessStatus{Name: name, Status: "stopped"}
	}
	return proc, fromWorktree
}

func isSingletonProcess(cfg *config.ProjectConfig, name string) bool {
	if cfg == nil {
		return false
	}
	processCfg, ok := cfg.Processes[name]
	if !ok {
		return false
	}
	singleton, ok := processCfg["singleton"].(bool)
	return ok && singleton
}

func formatProcessLine(proc daemon.ProcessStatus, fromWorktree string) string {
	base := ""
	if proc.Status == "running" && proc.PID > 0 {
		base = fmt.Sprintf("%s (running: PID %d)", proc.Name, proc.PID)
	} else if proc.PID > 0 {
		base = fmt.Sprintf("%s (%s: PID %d)", proc.Name, proc.Status, proc.PID)
	} else {
		base = fmt.Sprintf("%s (%s)", proc.Name, proc.Status)
	}
	fromWorktree = strings.TrimSpace(fromWorktree)
	if fromWorktree != "" {
		base += fmt.Sprintf(" (from worktree: %s)", fromWorktree)
	}
	return base
}

func mainWorktreeSlug(cfg *config.ProjectConfig) string {
	if cfg == nil {
		return "main"
	}
	if slug := strings.TrimSpace(cfg.Project.MainSlug); slug != "" {
		return slug
	}
	return "main"
}

func isMainWorktreePath(projectPath string) bool {
	if projectPath == "" {
		return false
	}
	mainPath, err := worktree.ResolveMainPathInDir(projectPath)
	if err != nil {
		return false
	}
	return samePath(projectPath, mainPath)
}

func shouldUseMainWorktreeStatus(cfg *config.ProjectConfig, projectPath string) bool {
	if cfg == nil || len(cfg.Processes) == 0 {
		return false
	}
	if isMainWorktreePath(projectPath) {
		return false
	}
	for name := range cfg.Processes {
		if isSingletonProcess(cfg, name) {
			return true
		}
	}
	return false
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	var last string
	for i, value := range values {
		if i == 0 || value != last {
			out = append(out, value)
		}
		last = value
	}
	return out
}

func resolveSlug(opts *Options, arg string) (string, error) {
	if arg != "" {
		if strings.Contains(arg, ":") {
			currentProject, err := resolveProjectFromCurrentDir(opts)
			if err != nil {
				return "", err
			}
			target, err := parseProjectSlugWithDefault(arg, currentProject)
			if err != nil {
				return "", err
			}
			if target.Project != currentProject {
				return "", fmt.Errorf("project mismatch: target %q does not match current repository %q", target.Project, currentProject)
			}
			return target.Slug, nil
		}
		return arg, nil
	}

	cwd := workingDir(opts)
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return "", err
	}
	return worktree.ResolveDefaultSlug(cwd, daemonCfg)
}

type processTarget struct {
	all       bool
	processes map[string]bool
}

func (t *processTarget) addProcess(name string) {
	if t.all {
		return
	}
	if t.processes == nil {
		t.processes = map[string]bool{}
	}
	t.processes[name] = true
}

func (t *processTarget) processList() []string {
	if t == nil || len(t.processes) == 0 {
		return nil
	}
	out := make([]string, 0, len(t.processes))
	for name := range t.processes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func resolveProcessTargets(args []string, opts *Options) (map[string]*processTarget, error) {
	targets := map[string]*processTarget{}
	if len(args) == 0 {
		slug, err := resolveSlug(opts, "")
		if err != nil {
			return nil, err
		}
		targets[slug] = &processTarget{all: true}
		return targets, nil
	}
	for _, arg := range args {
		id, err := worktree.ParseProcessIdentifier(arg)
		if err != nil {
			return nil, err
		}
		slug := id.Slug
		if slug == "" {
			resolved, err := resolveSlug(opts, "")
			if err != nil {
				return nil, err
			}
			slug = resolved
		}
		target, ok := targets[slug]
		if !ok {
			target = &processTarget{processes: map[string]bool{}}
			targets[slug] = target
		}
		if id.Process == "*" {
			target.all = true
			target.processes = nil
			continue
		}
		target.addProcess(id.Process)
	}
	return targets, nil
}

func sortedTargetSlugs(targets map[string]*processTarget) []string {
	slugs := make([]string, 0, len(targets))
	for slug := range targets {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}
