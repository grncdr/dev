package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev-mode/internal/config"
	"dev-mode/internal/daemon"
	"dev-mode/internal/worktree"
)

const (
	ansiBold  = "\x1b[1m"
	ansiReset = "\x1b[0m"
)

func bold(text string) string {
	return ansiBold + text + ansiReset
}

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

	targets, err := resolveProcessTargets(targetArgs)
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

	targets, err := resolveProcessTargets(targetArgs)
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

	targets, err := resolveProcessTargets(targetArgs)
	if err != nil {
		return err
	}

	client := daemon.NewClient(socketPath)
	daemonUp, err := daemonRunning(socketPath)
	if err != nil {
		return err
	}
	tunnelBySlug := map[string]daemon.TunnelStatus{}
	if daemonUp {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if tunnels, err := client.TunnelsStatus(ctx); err == nil {
			for _, t := range tunnels.Tunnels {
				existing, ok := tunnelBySlug[t.Slug]
				if !ok || (existing.Status != "connected" && t.Status == "connected") {
					tunnelBySlug[t.Slug] = t
				}
			}
		}
	}

	for i, slug := range sortedTargetSlugs(targets) {
		projectPath, _ := worktree.ResolvePathFromSlug(slug)
		var cfg *config.ProjectConfig
		var userCfg *config.UserConfig
		if projectPath != "" {
			cfgPath := filepath.Join(projectPath, config.DefaultProjectConfig)
			if loaded, _, err := config.LoadProjectConfig(cfgPath); err == nil {
				cfg = loaded
			}
		}
		if opts != nil && opts.ResolvedPaths.UserConfig != "" {
			if loaded, _, err := config.LoadUserConfig(opts.ResolvedPaths.UserConfig); err == nil {
				userCfg = loaded
			}
		}
		if i > 0 {
			fmt.Println()
		}
		printProjectSection(slug, projectPath, cfg)
		fmt.Println()

		if !daemonUp {
			printProcessesSection(nil, cfg, userCfg, slug, false, targets[slug], nil)
			fmt.Println()
			printGatewaySection(cfg, nil, false)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := client.WorktreeStatus(ctx, slug)
		cancel()
		if err != nil {
			printProcessesSection(nil, cfg, userCfg, slug, false, targets[slug], tunnelForSlug(tunnelBySlug, slug))
			fmt.Println()
			printGatewaySection(cfg, tunnelForSlug(tunnelBySlug, slug), true)
			continue
		}

		filtered := filterWorktreeStatus(resp, targets[slug])
		printProcessesSection(filtered, cfg, userCfg, slug, true, targets[slug], tunnelForSlug(tunnelBySlug, slug))
		fmt.Println()
		printGatewaySection(cfg, tunnelForSlug(tunnelBySlug, slug), true)
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

func printProjectSection(slug, path string, cfg *config.ProjectConfig) {
	fmt.Println(bold("Project"))
	if cfg != nil && cfg.Project.Name != "" {
		fmt.Printf("  name: %s\n", cfg.Project.Name)
	} else {
		fmt.Println("  name: (unknown)")
	}
	if path == "" {
		fmt.Println("  main worktree: (unknown)")
		return
	}
	mainPath, err := worktree.ResolveMainPathInDir(path)
	if err != nil {
		fmt.Println("  main worktree: (unknown)")
		return
	}
	if samePath(path, mainPath) {
		fmt.Println("  main worktree: this worktree")
		return
	}
	fmt.Printf("  main worktree: %s\n", mainPath)
}

func printProcessesSection(status *daemon.WorktreeStatus, cfg *config.ProjectConfig, userCfg *config.UserConfig, slug string, daemonUp bool, target *processTarget, tunnel *daemon.TunnelStatus) {
	fmt.Println(bold("Processes"))
	if !daemonUp {
		fmt.Println("  daemon not running")
		return
	}
	if status == nil || len(status.Processes) == 0 {
		fmt.Println("  (no processes)")
		return
	}
	proxyByProcess := processProxyURLsByProcess(slug, cfg, userCfg)
	gatewayByProcess := gatewayProxyURLsByProcess(proxyByProcess, cfg, tunnel)
	processes := append([]daemon.ProcessStatus(nil), status.Processes...)
	sort.Slice(processes, func(i, j int) bool { return processes[i].Name < processes[j].Name })
	if target != nil && !target.all && len(target.processes) > 0 {
		filtered := make([]daemon.ProcessStatus, 0, len(processes))
		for _, proc := range processes {
			if target.processes[proc.Name] {
				filtered = append(filtered, proc)
			}
		}
		processes = filtered
	}
	if len(processes) == 0 {
		fmt.Println("  (no processes)")
		return
	}
	for _, proc := range processes {
		if proc.PID > 0 {
			fmt.Printf("  %s: %s (pid %d)\n", proc.Name, proc.Status, proc.PID)
		} else {
			fmt.Printf("  %s: %s\n", proc.Name, proc.Status)
		}
		for _, route := range proxyByProcess[proc.Name] {
			fmt.Printf("    proxy %s\n", route)
		}
		for _, route := range gatewayByProcess[proc.Name] {
			fmt.Printf("    gateway %s\n", route)
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
	return &daemon.WorktreeStatus{Slug: status.Slug, Processes: filtered}
}

func processProxyURLsByProcess(slug string, cfg *config.ProjectConfig, userCfg *config.UserConfig) map[string][]string {
	result := map[string][]string{}
	if cfg == nil {
		return result
	}
	zone := strings.TrimPrefix(proxyApexZone(userCfg), ".")
	if zone == "" {
		zone = "localhost"
	}
	displaySlug := effectiveDisplaySlug(slug, cfg)
	for process, proc := range cfg.Processes {
		rawProxy, ok := proc["proxy"]
		if !ok || rawProxy == nil {
			continue
		}
		for _, matcher := range flattenProxyMatchers(rawProxy) {
			if tcpListen, ok := parseInt(rawProxyValue(matcher, "tcp_listen")); ok && tcpListen > 0 {
				result[process] = append(result[process], fmt.Sprintf("tcp://127.0.0.1:%d", tcpListen))
				continue
			}
			path := "/"
			if raw, ok := matcher["path"].(string); ok && raw != "" {
				path = raw
			}
			for _, subdomain := range matcherSubdomains(matcher) {
				host := fmt.Sprintf("%s.%s", displaySlug, zone)
				if subdomain == "*" {
					host = fmt.Sprintf("*.%s.%s", displaySlug, zone)
				} else if subdomain != "" {
					host = fmt.Sprintf("%s.%s.%s", subdomain, displaySlug, zone)
				}
				result[process] = append(result[process], fmt.Sprintf("https://%s%s", host, path))
			}
		}
	}
	for process, lines := range result {
		sort.Strings(lines)
		result[process] = dedupeStrings(lines)
	}
	return result
}

func matcherSubdomains(matcher map[string]any) []string {
	if matcher == nil {
		return []string{""}
	}
	if raw, ok := matcher["subdomains"]; ok {
		if list, ok := raw.([]any); ok {
			out := make([]string, 0, len(list))
			for _, item := range list {
				if item == nil {
					out = append(out, "")
					continue
				}
				if s, ok := item.(string); ok {
					out = append(out, strings.TrimSpace(s))
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	if raw, ok := matcher["subdomain"]; ok {
		if raw == nil {
			return []string{""}
		}
		if s, ok := raw.(string); ok {
			return []string{strings.TrimSpace(s)}
		}
	}
	return []string{""}
}

func proxyApexZone(userCfg *config.UserConfig) string {
	if userCfg != nil && strings.TrimSpace(userCfg.Proxy.ApexZone) != "" {
		return userCfg.Proxy.ApexZone
	}
	return ".localhost"
}

func effectiveDisplaySlug(slug string, cfg *config.ProjectConfig) string {
	displaySlug := slug
	if cfg == nil || cfg.Project.MainSlug == "" {
		return displaySlug
	}
	if path, err := worktree.ResolvePathFromSlug(slug); err == nil {
		if mainPath, err := worktree.ResolveMainPathInDir(path); err == nil && samePath(path, mainPath) {
			displaySlug = cfg.Project.MainSlug
		}
	}
	return displaySlug
}

func flattenProxyMatchers(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			out = append(out, flattenProxyMatchers(item)...)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	case map[string]any:
		return []map[string]any{typed}
	default:
		return nil
	}
}

func rawProxyValue(entry map[string]any, key string) any {
	if entry == nil {
		return nil
	}
	return entry[key]
}

func parseInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case uint:
		return int(value), true
	case uint64:
		return int(value), true
	case float64:
		return int(value), true
	case float32:
		return int(value), true
	default:
		return 0, false
	}
}

func samePath(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		return a == b
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return a == b
	}
	return aa == bb
}

func printGatewaySection(cfg *config.ProjectConfig, tunnel *daemon.TunnelStatus, daemonUp bool) {
	if cfg == nil {
		return
	}
	url, _ := cfg.Gateway["url"].(string)
	if strings.TrimSpace(url) == "" {
		return
	}
	fmt.Println(bold("Gateway"))
	fmt.Printf("  url: %s\n", url)
	if !daemonUp {
		fmt.Printf("  status: daemon not running\n")
		return
	}
	if tunnel == nil {
		fmt.Printf("  status: not connected\n")
		return
	}
	fmt.Printf("  status: %s\n", tunnel.Status)
	if tunnel.Label != "" {
		fmt.Printf("  label: %s\n", tunnel.Label)
		if public := gatewayPublicURL(url, tunnel.Label); public != "" {
			fmt.Printf("  public: %s\n", public)
		}
	}
	if tunnel.LastError != "" {
		fmt.Printf("  error: %s\n", tunnel.LastError)
	}
}

func tunnelForSlug(tunnels map[string]daemon.TunnelStatus, slug string) *daemon.TunnelStatus {
	if len(tunnels) == 0 {
		return nil
	}
	t, ok := tunnels[slug]
	if !ok {
		return nil
	}
	copy := t
	return &copy
}

func gatewayProxyURLsByProcess(localByProcess map[string][]string, cfg *config.ProjectConfig, tunnel *daemon.TunnelStatus) map[string][]string {
	out := map[string][]string{}
	if tunnel == nil || tunnel.Status != "connected" || cfg == nil {
		return out
	}
	gatewayURL, _ := cfg.Gateway["url"].(string)
	gatewayURL = strings.TrimSpace(gatewayURL)
	if gatewayURL == "" {
		return out
	}
	parsedGateway, err := url.Parse(gatewayURL)
	if err != nil || parsedGateway.Host == "" {
		return out
	}
	gatewayScheme := parsedGateway.Scheme
	if gatewayScheme == "" {
		gatewayScheme = "https"
	}
	gatewayHost := parsedGateway.Host
	label := strings.TrimSpace(tunnel.Label)
	if label == "" {
		return out
	}
	for process, routes := range localByProcess {
		for _, route := range routes {
			if !strings.HasPrefix(route, "https://") {
				continue
			}
			u, err := url.Parse(route)
			if err != nil || u.Host == "" {
				continue
			}
			host := u.Hostname()
			parts := strings.Split(host, ".")
			switch {
			case len(parts) == 2:
				u.Host = label + "." + gatewayHost
			case len(parts) >= 3:
				sub := strings.Join(parts[:len(parts)-2], ".")
				u.Host = sub + "." + label + "." + gatewayHost
			default:
				continue
			}
			u.Scheme = gatewayScheme
			out[process] = append(out[process], u.String())
		}
		sort.Strings(out[process])
		out[process] = dedupeStrings(out[process])
	}
	return out
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

func resolveSlug(arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	slug, err := worktree.ResolveSlug(cwd)
	if err != nil {
		return "", fmt.Errorf("slug is required: %w", err)
	}

	if strings.Contains(slug, "/") {
		return "", errors.New("resolved slug contains '/'; expected simple slug")
	}

	return slug, nil
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

func resolveProcessTargets(args []string) (map[string]*processTarget, error) {
	targets := map[string]*processTarget{}
	if len(args) == 0 {
		slug, err := resolveSlug("")
		if err != nil {
			return nil, err
		}
		targets[slug] = &processTarget{all: true}
		return targets, nil
	}
	for _, arg := range args {
		slug, process, _, err := parseAttachTarget(arg)
		if err != nil {
			return nil, err
		}
		if slug == "" {
			resolved, err := resolveSlug("")
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
		if process == "*" {
			target.all = true
			target.processes = nil
			continue
		}
		target.addProcess(process)
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
