package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/mattn/go-shellwords"

	"dev/internal/config"
	"dev/internal/procenv"
	"dev/internal/router"
	"dev/internal/worktree"
)

// Manager owns in-process daemon runtime state and lifecycle operations.
//
// It is the control-plane backend used by Server and is intentionally focused
// on orchestration, not network transport handling.
type Manager struct {
	mu                   sync.Mutex
	router               *router.Router
	processes            map[string]map[string]*processInfo
	worktrees            map[string]runtimeWorktree
	slugIndex            map[string]map[string]struct{}
	dnsLabelIndex        map[string]map[string]struct{}
	pendingProxySessions map[string]map[string]int
	apexZone             string
	daemonCfg            *config.DaemonConfig
}

const defaultProxyProcessIdleTimeout = 5 * time.Minute

var errWorktreeNotRunning = errors.New("worktree not running")

// NewManager creates the daemon runtime manager.
//
// Manager is responsible for local orchestration/state:
// - worktree registration/runtime indexes
// - process start/stop/readiness and proxy session tracking
// - path/slug resolution for running worktrees
//
// Manager does not own external transports; Server calls into Manager.
func NewManager() *Manager {
	return &Manager{
		router:               router.New(".localhost"),
		processes:            make(map[string]map[string]*processInfo),
		worktrees:            make(map[string]runtimeWorktree),
		slugIndex:            make(map[string]map[string]struct{}),
		dnsLabelIndex:        make(map[string]map[string]struct{}),
		pendingProxySessions: make(map[string]map[string]int),
		apexZone:             ".localhost",
	}
}

type runtimeWorktree struct {
	Slug     string
	Project  string
	Path     string
	DNSLabel string
}

func (m *Manager) SetApexZone(zone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apexZone = zone
	if m.router != nil {
		m.router.SetApexZone(zone)
	}
}

func (m *Manager) SetDaemonConfig(cfg *config.DaemonConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.daemonCfg = cfg
	m.rebuildRouterLocked()
}

type WorktreeStatus struct {
	Project   string          `json:"project,omitempty"`
	Slug      string          `json:"slug"`
	Path      string          `json:"path,omitempty"`
	Processes []ProcessStatus `json:"processes"`
	Routing   WorktreeRouting `json:"routing,omitempty"`
}

type ProcessStatus struct {
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Status string `json:"status"`
}

type WorktreeRouting struct {
	Local         map[string][]string `json:"local,omitempty"`
	Gateway       map[string][]string `json:"gateway,omitempty"`
	GatewayURL    string              `json:"gateway_url,omitempty"`
	GatewayStatus string              `json:"gateway_status,omitempty"`
}

type processInfo struct {
	cmd            *exec.Cmd
	network        string
	address        string
	health         *processHealthCheck
	startupTimeout time.Duration
	idleTimeout    time.Duration
	lastActivity   time.Time
	activeProxies  int
	idleTimer      *time.Timer
	pty            *os.File
	logFile        *os.File
	logSize        int64
	ready          bool
	readyErr       error
	readyWait      chan struct{}
	exited         chan struct{} // closed when process exits
	exitErr        error         // exit error from Wait()
	mu             sync.Mutex
	subs           map[int]io.Writer
	nextID         int
}

func (m *Manager) StartWorktree(slug string) (*WorktreeStatus, error) {
	return m.startWorktreeFromRef(slug, "", "", nil, true)
}

func (m *Manager) StartWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	return m.startWorktreeFromRef(slug, "", dirHint, nil, true)
}

func (m *Manager) StartWorktreeFromRef(slug, project, dirHint string) (*WorktreeStatus, error) {
	return m.startWorktreeFromRef(slug, project, dirHint, nil, true)
}

func (m *Manager) StartProcessesFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.startWorktreeFromRef(slug, "", dirHint, processes, all)
}

func (m *Manager) StartProcessesFromRef(slug, project, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.startWorktreeFromRef(slug, project, dirHint, processes, all)
}

func (m *Manager) startWorktreeFromRef(slug, project, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	path, err := m.resolveWorktreePath(slug, project, dirHint)
	if err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	mainPath, err := worktree.ResolveMainPathInDir(path)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveProjectIdentifier(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve project identifier: %w", err)
	}
	runtimeKey := runtimeKeyForPath(path)
	dnsLabel := worktree.ProxyDNSLabelForSlug(cfg, slug)
	branch, err := resolveWorktreeBranch(path)
	if err != nil {
		return nil, err
	}
	isMain := sameResolvedPath(path, mainPath)
	mainSlug := slug
	mainRuntimeKey := runtimeKey
	if !isMain {
		mainSlug, err = worktree.ResolveMainSlug(mainPath)
		if err != nil {
			return nil, fmt.Errorf("resolve main worktree slug: %w", err)
		}
		mainRuntimeKey = runtimeKeyForPath(mainPath)
	}

	worktreeState, err := resolveWorktreeState(cfg.Project.Name, slug)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(worktreeState, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree state dir: %w", err)
	}

	m.mu.Lock()
	apexZone := m.apexZone
	m.mu.Unlock()
	runtimeVars := buildRuntimeVars(cfg, projectID, slug, path, branch, apexZone)
	templateVars := buildTemplateVars(runtimeVars, mainPath, worktreeState)
	if err := runHook(cfg.Hooks.PreStart, cfg.Commands.Wrapper, "pre_start", path, runtimeVars); err != nil {
		return nil, err
	}
	statuses := []ProcessStatus{}

	selected := selectProcesses(cfg.Processes, processes, all)
	needs, err := buildProcessNeeds(cfg.Processes)
	if err != nil {
		return nil, err
	}
	localSet, mainSet, err := needs.buildStartSets(selected, isMain)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	hostConflicts := m.localDNSHostConflictsLocked(runtimeKey, cfg, slug, localSet, apexZone)
	m.mu.Unlock()
	if len(hostConflicts) > 0 {
		return nil, formatLocalDNSConflictError(hostConflicts)
	}

	m.mu.Lock()
	if _, ok := m.processes[runtimeKey]; !ok {
		m.processes[runtimeKey] = make(map[string]*processInfo)
	}
	m.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     slug,
		Project:  projectID,
		Path:     path,
		DNSLabel: dnsLabel,
	})
	m.mu.Unlock()

	if !isMain && len(mainSet) > 0 {
		mainNames := make([]string, 0, len(mainSet))
		for name := range mainSet {
			mainNames = append(mainNames, name)
		}
		sort.Strings(mainNames)
		if _, err := m.StartProcessesFromDir(mainSlug, mainPath, mainNames, false); err != nil {
			return nil, err
		}
	}

	localOrder, err := topoSortProcesses(localSet, needs.needs)
	if err != nil {
		return nil, err
	}
	for _, name := range localOrder {
		proc := cfg.Processes[name]
		if singleton, ok := proc["singleton"].(bool); ok && singleton && !isMain {
			continue
		}

		network, address, port, err := resolveProcessTarget(proc, name, worktreeState)
		if err != nil {
			return nil, fmt.Errorf("resolve port for %s: %w", name, err)
		}
		depPortVars, err := m.dependencyPortEnvVars(name, needs, runtimeKey, mainRuntimeKey, isMain)
		if err != nil {
			return nil, err
		}
		procVars := procenv.CloneEnv(templateVars)
		for key, value := range depPortVars {
			procVars[key] = value
		}
		if port != "" {
			procVars["PORT"] = port
		}

		command, ok := proc["command"].(string)
		if !ok {
			continue
		}

		command = expandVars(command, procVars)
		args, err := shellwords.Parse(command)
		if err != nil {
			return nil, fmt.Errorf("parse command for %s: %w", name, err)
		}
		if len(args) == 0 {
			continue
		}

		wrapper := ""
		if procWrapper, ok := proc["wrapper"].(string); ok && procWrapper != "" {
			wrapper = procWrapper
		} else if cfg.Commands.Wrapper != "" {
			wrapper = cfg.Commands.Wrapper
		}
		if wrapper != "" {
			args, err = procenv.ApplyWrapper(wrapper, args)
			if err != nil {
				return nil, fmt.Errorf("apply wrapper for %s: %w", name, err)
			}
		}

		m.mu.Lock()
		if existing := m.processes[runtimeKey][name]; existing != nil && existing.cmd != nil && existing.cmd.Process != nil && !existing.hasExited() {
			statuses = append(statuses, ProcessStatus{Name: name, PID: existing.cmd.Process.Pid, Status: "running"})
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()

		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(), procenv.FormatEnv(runtimeVars)...)
		cmd.Env = append(cmd.Env, procenv.FormatEnv(depPortVars)...)
		configureManagedProcess(cmd)

		if envVars, ok := proc["env"].(map[string]any); ok {
			for key, raw := range envVars {
				val, ok := raw.(string)
				if !ok {
					continue
				}
				val = expandVars(val, procVars)
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, val))
			}
		}
		if port != "" {
			cmd.Env = append(cmd.Env,
				fmt.Sprintf("PORT=%s", port),
				fmt.Sprintf("DEV_PORT=%s", port),
				fmt.Sprintf("DEV_PORT_%s=%s", envKey(name), port),
			)
		}
		if network == "unix" && address != "" {
			cmd.Env = append(cmd.Env,
				fmt.Sprintf("DEV_SOCKET=%s", address),
				fmt.Sprintf("DEV_SOCKET_%s=%s", envKey(name), address),
			)
		}

		logPath := filepath.Join(worktreeState, name+".log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		logSize, err := trimOpenFileToMax(logFile, managedLogMaxBytes)
		if err != nil {
			_ = logFile.Close()
			return nil, fmt.Errorf("trim log file: %w", err)
		}

		ptmx, err := pty.Start(cmd)
		if err != nil {
			_ = logFile.Close()
			return nil, err
		}

		m.mu.Lock()
		info := &processInfo{
			cmd:            cmd,
			network:        network,
			address:        address,
			health:         parseProcessHealth(proc["health"]),
			startupTimeout: parseProcessStartupTimeout(proc["startup_timeout"]),
			idleTimeout:    resolveProcessIdleTimeout(proc),
			lastActivity:   time.Now(),
			pty:            ptmx,
			logFile:        logFile,
			logSize:        logSize,
			exited:         make(chan struct{}),
			subs:           make(map[int]io.Writer),
		}
		info.activeProxies = m.consumePendingProxySessionsLocked(runtimeKey, name)
		info.startOutputPump()
		m.processes[runtimeKey][name] = info
		m.mu.Unlock()
		m.rescheduleIdleTimer(runtimeKey, name, info)

		logProcessEvent("start", slug, name, cmd.Process.Pid, network, address)

		go func(slugName, procName string, proc *exec.Cmd, meta *processInfo) {
			err := proc.Wait()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
				}
			}
			meta.mu.Lock()
			meta.exitErr = err
			if meta.idleTimer != nil {
				meta.idleTimer.Stop()
				meta.idleTimer = nil
			}
			meta.mu.Unlock()
			close(meta.exited)
			logProcessExit(slugName, procName, proc.Process.Pid, exitCode, err)
			if meta.pty != nil {
				_ = meta.pty.Close()
			}
			if meta.logFile != nil {
				_ = meta.logFile.Close()
			}
		}(slug, name, cmd, info)

		statuses = append(statuses, ProcessStatus{Name: name, PID: cmd.Process.Pid, Status: "running"})
	}

	if err := runHook(cfg.Hooks.PostStart, cfg.Commands.Wrapper, "post_start", path, runtimeVars); err != nil {
		return nil, err
	}

	return &WorktreeStatus{Project: projectID, Slug: slug, Path: path, Processes: statuses}, nil
}

func (m *Manager) resolveWorktreePath(slug, project, dirHint string) (string, error) {
	m.mu.Lock()
	daemonCfg := m.daemonCfg
	m.mu.Unlock()
	return worktree.ResolvePathWithProjectHint(slug, project, dirHint, daemonCfg)
}

func runtimeKeyForPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(trimmed)
	if err == nil && strings.TrimSpace(resolved) != "" {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(trimmed)
}

func (m *Manager) registerWorktreeLocked(key string, wt runtimeWorktree) {
	if key == "" {
		return
	}
	if previous, ok := m.worktrees[key]; ok {
		m.removeIndexLocked(m.slugIndex, previous.Slug, key)
		m.removeIndexLocked(m.dnsLabelIndex, previous.DNSLabel, key)
	}
	m.worktrees[key] = wt
	m.addIndexLocked(m.slugIndex, wt.Slug, key)
	m.addIndexLocked(m.dnsLabelIndex, wt.DNSLabel, key)
	m.rebuildRouterLocked()
}

func (m *Manager) addIndexLocked(index map[string]map[string]struct{}, value, key string) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || key == "" {
		return
	}
	entries, ok := index[value]
	if !ok {
		entries = map[string]struct{}{}
		index[value] = entries
	}
	entries[key] = struct{}{}
}

func (m *Manager) removeIndexLocked(index map[string]map[string]struct{}, value, key string) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || key == "" {
		return
	}
	entries, ok := index[value]
	if !ok {
		return
	}
	delete(entries, key)
	if len(entries) == 0 {
		delete(index, value)
		return
	}
	index[value] = entries
}

func (m *Manager) rebuildRouterLocked() {
	if m.router == nil {
		m.router = router.New(m.apexZone)
	} else {
		m.router = router.New(m.apexZone)
	}

	addWorktree := func(runtimeKey string, wt runtimeWorktree) {
		in, ok := m.routerWorktreeInputLocked(runtimeKey, wt)
		if !ok {
			return
		}
		m.router.UpsertWorktree(in)
	}

	for runtimeKey, wt := range m.worktrees {
		addWorktree(runtimeKey, wt)
	}

	registered, err := worktree.ListRegisteredWorktrees(m.daemonCfg, "")
	if err != nil {
		return
	}
	for _, entry := range registered {
		runtimeKey := runtimeKeyForPath(entry.Path)
		if runtimeKey == "" {
			continue
		}
		wt := runtimeWorktree{
			Slug:    entry.Slug,
			Project: entry.Project,
			Path:    entry.Path,
		}
		addWorktree(runtimeKey, wt)
	}
}

func (m *Manager) routerWorktreeInputLocked(runtimeKey string, wt runtimeWorktree) (router.WorktreeInput, bool) {
	runtimeKey = strings.TrimSpace(runtimeKey)
	if runtimeKey == "" || strings.TrimSpace(wt.Slug) == "" || strings.TrimSpace(wt.Path) == "" {
		return router.WorktreeInput{}, false
	}
	cfgPath := filepath.Join(wt.Path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return router.WorktreeInput{}, false
	}
	labels := routerLabelsForWorktree(wt, cfg)
	matchers := routerMatchersForConfig(cfg)
	if len(labels) == 0 || len(matchers) == 0 {
		return router.WorktreeInput{}, false
	}
	return router.WorktreeInput{
		RuntimeKey: runtimeKey,
		Slug:       wt.Slug,
		RepoPath:   wt.Path,
		Labels:     labels,
		Matchers:   matchers,
	}, true
}

func routerLabelsForWorktree(wt runtimeWorktree, cfg *config.ProjectConfig) []string {
	labels := map[string]struct{}{}
	add := func(label string) {
		label = strings.TrimSpace(strings.ToLower(label))
		if label == "" {
			return
		}
		labels[label] = struct{}{}
	}

	add(wt.DNSLabel)
	add(worktree.SlugDNSLabel(wt.Slug))
	add(worktree.ProxyDNSLabelForSlug(cfg, wt.Slug))

	mainSlug := "main"
	if cfg != nil {
		if configured, err := worktree.NormalizeIdentifierSegment(cfg.Project.MainSlug); err == nil && configured != "" {
			mainSlug = configured
		}
	}
	if strings.EqualFold(wt.Slug, "main") || strings.EqualFold(wt.Slug, mainSlug) {
		add("main")
		add(worktree.SlugDNSLabel(mainSlug))
		add(worktree.ProxyDNSLabelForSlug(cfg, "main"))
		add(worktree.ProxyDNSLabelForSlug(cfg, mainSlug))
	}

	out := make([]string, 0, len(labels))
	for label := range labels {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func routerMatchersForConfig(cfg *config.ProjectConfig) []router.Matcher {
	return router.ParseMatchers(cfg)
}

type localDNSHostConflict struct {
	Host         string
	ExistingSlug string
	ExistingPath string
}

func (m *Manager) localDNSHostConflictsLocked(runtimeKey string, cfg *config.ProjectConfig, slug string, selected map[string]bool, apexZone string) []localDNSHostConflict {
	candidateHosts := proxyHostsForConfig(cfg, slug, selected, apexZone)
	if len(candidateHosts) == 0 {
		return nil
	}

	conflicts := []localDNSHostConflict{}
	for key, wt := range m.worktrees {
		if key == runtimeKey {
			continue
		}
		procs, ok := m.processes[key]
		if !ok || len(procs) == 0 {
			continue
		}
		existingSelected := map[string]bool{}
		for name, info := range procs {
			if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
				continue
			}
			existingSelected[name] = true
		}
		if len(existingSelected) == 0 {
			continue
		}
		existingCfgPath := filepath.Join(wt.Path, config.DefaultProjectConfig)
		existingCfg, _, err := config.LoadProjectConfig(existingCfgPath)
		if err != nil {
			continue
		}
		existingHosts := proxyHostsForConfig(existingCfg, wt.Slug, existingSelected, apexZone)
		for host := range candidateHosts {
			if _, ok := existingHosts[host]; ok {
				conflicts = append(conflicts, localDNSHostConflict{
					Host:         host,
					ExistingSlug: wt.Slug,
					ExistingPath: wt.Path,
				})
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Host != conflicts[j].Host {
			return conflicts[i].Host < conflicts[j].Host
		}
		if conflicts[i].ExistingSlug != conflicts[j].ExistingSlug {
			return conflicts[i].ExistingSlug < conflicts[j].ExistingSlug
		}
		return conflicts[i].ExistingPath < conflicts[j].ExistingPath
	})
	return dedupeLocalDNSConflicts(conflicts)
}

func proxyHostsForConfig(cfg *config.ProjectConfig, slug string, selected map[string]bool, apexZone string) map[string]struct{} {
	hosts := map[string]struct{}{}
	if cfg == nil || len(selected) == 0 {
		return hosts
	}
	routeSlug := localProxyRouteSlug(slug, cfg)
	for process, routes := range localProxyRoutesByProcess(router.ParseMatchers(cfg), routeSlug, apexZone) {
		if !selected[process] {
			continue
		}
		for _, route := range routes {
			if !strings.HasPrefix(route, "https://") {
				continue
			}
			hostPath := strings.TrimPrefix(route, "https://")
			host, _, _ := strings.Cut(hostPath, "/")
			host = strings.TrimSpace(strings.ToLower(host))
			if host == "" {
				continue
			}
			hosts[host] = struct{}{}
		}
	}
	return hosts
}

func dedupeLocalDNSConflicts(conflicts []localDNSHostConflict) []localDNSHostConflict {
	if len(conflicts) == 0 {
		return conflicts
	}
	out := make([]localDNSHostConflict, 0, len(conflicts))
	last := localDNSHostConflict{}
	hasLast := false
	for _, conflict := range conflicts {
		if hasLast && conflict == last {
			continue
		}
		out = append(out, conflict)
		last = conflict
		hasLast = true
	}
	return out
}

func formatLocalDNSConflictError(conflicts []localDNSHostConflict) error {
	if len(conflicts) == 0 {
		return nil
	}
	parts := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict.ExistingPath != "" {
			parts = append(parts, fmt.Sprintf("%s (already routed to slug %q at %s)", conflict.Host, conflict.ExistingSlug, conflict.ExistingPath))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (already routed to slug %q)", conflict.Host, conflict.ExistingSlug))
	}
	return fmt.Errorf("local DNS host conflict: %s. Configure `local-dns.overrides` in `.dev.toml` or `.dev.local.toml` to map this worktree slug to a unique host label", strings.Join(parts, ", "))
}

func resolveProjectIdentifier(cfg *config.ProjectConfig) (string, error) {
	if cfg == nil {
		return "", errors.New("missing project config")
	}
	return worktree.NormalizeIdentifierSegment(cfg.Project.Name)
}

func selectProcesses(allProcesses map[string]map[string]any, names []string, all bool) map[string]map[string]any {
	if all || len(names) == 0 {
		return allProcesses
	}
	selected := map[string]map[string]any{}
	for _, name := range names {
		if proc, ok := allProcesses[name]; ok {
			selected[name] = proc
		}
	}
	return selected
}

type processNeeds struct {
	needs       map[string][]string
	singleton   map[string]bool
	processes   map[string]map[string]any
	parsedCache map[string][]string
}

func buildProcessNeeds(processes map[string]map[string]any) (*processNeeds, error) {
	out := &processNeeds{
		needs:     make(map[string][]string, len(processes)),
		singleton: make(map[string]bool, len(processes)),
		processes: processes,
	}
	for name, proc := range processes {
		if rawSingleton, ok := proc["singleton"].(bool); ok {
			out.singleton[name] = rawSingleton
		}
		parsed, err := config.ParseProcessNeeds(proc["needs"])
		if err != nil {
			return nil, fmt.Errorf("process %s needs: %w", name, err)
		}
		out.needs[name] = parsed
	}
	return out, nil
}

func (p *processNeeds) ensureExists(parent, name string) error {
	if _, ok := p.processes[name]; !ok {
		if parent == "" {
			return fmt.Errorf("process needs unknown process %s", name)
		}
		return fmt.Errorf("process %s needs unknown process %s", parent, name)
	}
	return nil
}

func (p *processNeeds) buildStartSets(selected map[string]map[string]any, isMain bool) (localSet, mainSet map[string]bool, err error) {
	localSet = map[string]bool{}
	mainSet = map[string]bool{}
	var addMain func(parent, name string) error
	addMain = func(parent, name string) error {
		if mainSet[name] {
			return nil
		}
		if err := p.ensureExists(parent, name); err != nil {
			return err
		}
		mainSet[name] = true
		for _, dep := range p.needs[name] {
			if err := addMain(name, dep); err != nil {
				return err
			}
		}
		return nil
	}
	var addLocal func(parent, name string) error
	addLocal = func(parent, name string) error {
		if localSet[name] {
			return nil
		}
		if err := p.ensureExists(parent, name); err != nil {
			return err
		}
		if p.singleton[name] && !isMain {
			return addMain(parent, name)
		}
		localSet[name] = true
		for _, dep := range p.needs[name] {
			if err := addLocal(name, dep); err != nil {
				return err
			}
		}
		return nil
	}
	for name := range selected {
		if isMain {
			if err := addLocal("", name); err != nil {
				return nil, nil, err
			}
			continue
		}
		if p.singleton[name] {
			if err := addMain("", name); err != nil {
				return nil, nil, err
			}
			continue
		}
		if err := addLocal("", name); err != nil {
			return nil, nil, err
		}
	}
	return localSet, mainSet, nil
}

func topoSortProcesses(startSet map[string]bool, needs map[string][]string) ([]string, error) {
	if len(startSet) == 0 {
		return nil, nil
	}
	order := []string{}
	visiting := map[string]bool{}
	visited := map[string]bool{}

	var visit func(name string, stack []string) error
	visit = func(name string, stack []string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("process dependency cycle: %s -> %s", strings.Join(stack, " -> "), name)
		}
		visiting[name] = true
		stack = append(stack, name)
		for _, dep := range needs[name] {
			if !startSet[dep] {
				continue
			}
			if err := visit(dep, stack); err != nil {
				return err
			}
		}
		visiting[name] = false
		visited[name] = true
		order = append(order, name)
		return nil
	}

	names := make([]string, 0, len(startSet))
	for name := range startSet {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := visit(name, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (m *Manager) StopWorktree(slug string) (*WorktreeStatus, error) {
	return m.stopWorktreeFromRef(slug, "", "", nil, true)
}

func (m *Manager) StopWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	return m.stopWorktreeFromRef(slug, "", dirHint, nil, true)
}

func (m *Manager) StopWorktreeFromRef(slug, project, dirHint string) (*WorktreeStatus, error) {
	return m.stopWorktreeFromRef(slug, project, dirHint, nil, true)
}

func (m *Manager) StopProcessesFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.stopWorktreeFromRef(slug, "", dirHint, processes, all)
}

func (m *Manager) StopProcessesFromRef(slug, project, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.stopWorktreeFromRef(slug, project, dirHint, processes, all)
}

func (m *Manager) stopWorktreeFromRef(slug, project, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	path, err := m.resolveWorktreePath(slug, project, dirHint)
	if err != nil {
		return nil, err
	}

	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	projectID, err := resolveProjectIdentifier(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve project identifier: %w", err)
	}
	branch, err := resolveWorktreeBranch(path)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	apexZone := m.apexZone
	m.mu.Unlock()
	runtimeVars := buildRuntimeVars(cfg, projectID, slug, path, branch, apexZone)

	if err := runHook(cfg.Hooks.PreStop, cfg.Commands.Wrapper, "pre_stop", path, runtimeVars); err != nil {
		return nil, err
	}
	runtimeKey := runtimeKeyForPath(path)
	dnsLabel := worktree.ProxyDNSLabelForSlug(cfg, slug)
	m.mu.Lock()
	m.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     slug,
		Project:  projectID,
		Path:     path,
		DNSLabel: dnsLabel,
	})
	m.mu.Unlock()

	m.mu.Lock()
	procs, ok := m.processes[runtimeKey]
	m.mu.Unlock()

	statuses := []ProcessStatus{}
	if !ok {
		return &WorktreeStatus{Project: projectID, Slug: slug, Path: path, Processes: statuses}, nil
	}

	selected := selectProcesses(cfg.Processes, processes, all)
	for name, info := range procs {
		if _, ok := selected[name]; !ok {
			continue
		}
		if info == nil || info.cmd == nil || info.cmd.Process == nil {
			statuses = append(statuses, ProcessStatus{Name: name, Status: "stopped"})
			continue
		}
		stopManagedProcess(info)
		statuses = append(statuses, ProcessStatus{Name: name, PID: info.cmd.Process.Pid, Status: "stopped"})
	}

	m.mu.Lock()
	if existing, ok := m.processes[runtimeKey]; ok {
		for name := range selected {
			delete(existing, name)
		}
		if len(existing) == 0 {
			delete(m.processes, runtimeKey)
			m.unregisterWorktreeLocked(runtimeKey)
		} else {
			m.processes[runtimeKey] = existing
		}
	}
	m.mu.Unlock()

	if err := runHook(cfg.Hooks.PostStop, cfg.Commands.Wrapper, "post_stop", path, runtimeVars); err != nil {
		return nil, err
	}

	return &WorktreeStatus{Project: projectID, Slug: slug, Path: path, Processes: statuses}, nil
}

func (m *Manager) StatusWorktree(slug string) (*WorktreeStatus, error) {
	return m.StatusWorktreeFromRef(slug, "", "")
}

func (m *Manager) StatusWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	return m.StatusWorktreeFromRef(slug, "", dirHint)
}

func (m *Manager) StatusWorktreeFromRef(slug, project, dirHint string) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	path, err := m.resolveWorktreePath(slug, project, dirHint)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveProjectIdentifier(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve project identifier: %w", err)
	}

	runtimeKey := runtimeKeyForPath(path)
	dnsLabel := worktree.ProxyDNSLabelForSlug(cfg, slug)
	m.mu.Lock()
	m.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     slug,
		Project:  projectID,
		Path:     path,
		DNSLabel: dnsLabel,
	})
	m.mu.Unlock()

	m.mu.Lock()
	procs, ok := m.processes[runtimeKey]
	// Copy the map entries under the lock so we can iterate without racing
	// against stopIdleProcess which deletes from the map.
	type procEntry struct {
		name string
		info *processInfo
	}
	var entries []procEntry
	for name, info := range procs {
		entries = append(entries, procEntry{name, info})
	}
	m.mu.Unlock()

	statuses := []ProcessStatus{}
	if !ok {
		return &WorktreeStatus{Project: projectID, Slug: slug, Path: path, Processes: statuses}, nil
	}

	for _, e := range entries {
		name, info := e.name, e.info
		status := "unknown"
		pid := 0
		if info != nil && info.cmd != nil && info.cmd.Process != nil {
			pid = info.cmd.Process.Pid
			if info.hasExited() {
				status = "exited"
			} else {
				status = "running"
			}
		}
		statuses = append(statuses, ProcessStatus{Name: name, PID: pid, Status: status})
	}

	return &WorktreeStatus{Project: projectID, Slug: slug, Path: path, Processes: statuses}, nil
}

func resolveWorktreeState(project, slug string) (string, error) {
	base, err := config.ResolveStateDir(nil)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "logs", project, slug), nil
}

func normalizeRuntimeIndexValue(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func (m *Manager) runtimeKeyForSlugLocked(slug string) (string, error) {
	normalized := normalizeRuntimeIndexValue(slug)
	if normalized == "" {
		return "", errors.New("slug is required")
	}
	keys, ok := m.slugIndex[normalized]
	if !ok || len(keys) == 0 {
		return "", errWorktreeNotRunning
	}
	if len(keys) == 1 {
		for key := range keys {
			return key, nil
		}
	}
	paths := make([]string, 0, len(keys))
	for key := range keys {
		if wt, ok := m.worktrees[key]; ok && wt.Path != "" {
			paths = append(paths, wt.Path)
		} else {
			paths = append(paths, key)
		}
	}
	sort.Strings(paths)
	return "", fmt.Errorf("worktree slug %q is running in multiple paths (%s); use a local DNS override in .dev.toml or .dev.local.toml to disambiguate", slug, strings.Join(paths, ", "))
}

func (m *Manager) runtimeKeyForTargetLocked(slug, dirHint string) (string, error) {
	key := runtimeKeyForPath(dirHint)
	if key != "" {
		if _, ok := m.processes[key]; ok {
			return key, nil
		}
		if _, ok := m.worktrees[key]; ok {
			return key, nil
		}
	}
	return m.runtimeKeyForSlugLocked(slug)
}

func (m *Manager) unregisterWorktreeLocked(key string) {
	wt, ok := m.worktrees[key]
	if !ok {
		return
	}
	delete(m.worktrees, key)
	m.removeIndexLocked(m.slugIndex, wt.Slug, key)
	m.removeIndexLocked(m.dnsLabelIndex, wt.DNSLabel, key)
	m.rebuildRouterLocked()
}

func (m *Manager) TargetFor(slug, process string) (network string, address string, err error) {
	return m.TargetForFromDir(slug, "", process)
}

func (m *Manager) TargetForFromDir(slug, dirHint, process string) (network string, address string, err error) {
	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForTargetLocked(slug, dirHint)
	if keyErr != nil {
		m.mu.Unlock()
		return "", "", keyErr
	}
	defer m.mu.Unlock()
	procs, ok := m.processes[runtimeKey]
	if !ok {
		return "", "", errors.New("worktree not running")
	}
	info, ok := procs[process]
	if !ok || info == nil || info.address == "" || info.network == "" {
		return "", "", errors.New("socket not found for process")
	}
	return info.network, info.address, nil
}

func (m *Manager) EnsureProcessForTarget(slug, process string) (network string, address string, err error) {
	return m.EnsureProcessForTargetFromDir(slug, "", process)
}

func (m *Manager) EnsureProcessForTargetFromDir(slug, dirHint, process string) (network string, address string, err error) {
	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForTargetLocked(slug, dirHint)
	procs := map[string]*processInfo(nil)
	ok := false
	if keyErr == nil {
		procs, ok = m.processes[runtimeKey]
	}
	var info *processInfo
	if ok {
		info = procs[process]
	}
	m.mu.Unlock()
	if keyErr != nil && !errors.Is(keyErr, errWorktreeNotRunning) {
		return "", "", keyErr
	}
	if ok && info != nil && info.address != "" && info.network != "" && info.cmd != nil && info.cmd.Process != nil && !info.hasExited() {
		if err := waitForProcessReady(info); err != nil {
			return "", "", err
		}
		return info.network, info.address, nil
	}
	if ok && info != nil && info.cmd != nil && info.cmd.Process != nil && (info.address == "" || info.network == "") {
		return "", "", errors.New("process is running without proxy target; set port = \"unix\", \"random\", or an integer")
	}
	if strings.TrimSpace(dirHint) == "" {
		dirHint, _ = m.WorktreePath(slug)
	}
	status, err := m.StartWorktreeFromDir(slug, dirHint)
	if err != nil {
		return "", "", err
	}
	for _, proc := range status.Processes {
		if proc.Name == process {
			network, address, err := m.TargetForFromDir(slug, dirHint, process)
			if err != nil {
				return "", "", err
			}
			m.mu.Lock()
			currentKey, keyErr := m.runtimeKeyForTargetLocked(slug, dirHint)
			if keyErr != nil {
				m.mu.Unlock()
				return "", "", keyErr
			}
			current := m.processes[currentKey][process]
			m.mu.Unlock()
			if err := waitForProcessReady(current); err != nil {
				return "", "", err
			}
			return network, address, nil
		}
	}
	return "", "", errors.New("socket not found for process")
}

func (m *Manager) WorktreePath(slug string) (string, bool) {
	return m.WorktreePathFromDir(slug, "")
}

func (m *Manager) WorktreeRecords() []runtimeWorktree {
	m.mu.Lock()
	defer m.mu.Unlock()

	records := make([]runtimeWorktree, 0, len(m.worktrees))
	for _, wt := range m.worktrees {
		if strings.TrimSpace(wt.Slug) == "" || strings.TrimSpace(wt.Path) == "" {
			continue
		}
		records = append(records, wt)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].DNSLabel != records[j].DNSLabel {
			return records[i].DNSLabel < records[j].DNSLabel
		}
		if records[i].Slug != records[j].Slug {
			return records[i].Slug < records[j].Slug
		}
		return records[i].Path < records[j].Path
	})
	return records
}

func (m *Manager) WorktreePathFromDir(slug, dirHint string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtimeKey, err := m.runtimeKeyForTargetLocked(slug, dirHint)
	if err != nil {
		return "", false
	}
	wt, ok := m.worktrees[runtimeKey]
	if !ok {
		return "", false
	}
	return wt.Path, strings.TrimSpace(wt.Path) != ""
}

func (m *Manager) StopAllWorktrees() {
	entries := m.runningWorktrees()
	for _, entry := range entries {
		_, _ = m.StopWorktreeFromDir(entry.Slug, entry.Path)
	}
}

func (m *Manager) beginProxySession(slug, process string) {
	m.beginProxySessionFromDir(slug, "", process)
}

func (m *Manager) beginProxySessionFromDir(slug, dirHint, process string) {
	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForTargetLocked(slug, dirHint)
	if keyErr != nil {
		m.mu.Unlock()
		return
	}
	info := m.processInfoLocked(runtimeKey, process)
	if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
		if _, ok := m.pendingProxySessions[runtimeKey]; !ok {
			m.pendingProxySessions[runtimeKey] = make(map[string]int)
		}
		m.pendingProxySessions[runtimeKey][process]++
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	info.mu.Lock()
	if info.idleTimeout <= 0 || info.hasExited() {
		info.mu.Unlock()
		return
	}
	if info.idleTimer != nil {
		info.idleTimer.Stop()
		info.idleTimer = nil
	}
	info.activeProxies++
	info.lastActivity = time.Now()
	info.mu.Unlock()
}

func (m *Manager) endProxySession(slug, process string) {
	m.endProxySessionFromDir(slug, "", process)
}

func (m *Manager) endProxySessionFromDir(slug, dirHint, process string) {
	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForTargetLocked(slug, dirHint)
	if keyErr != nil {
		m.mu.Unlock()
		return
	}
	info := m.processInfoLocked(runtimeKey, process)
	if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
		m.decrementPendingProxySessionLocked(runtimeKey, process)
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	info.mu.Lock()
	if info.idleTimeout <= 0 || info.hasExited() {
		info.mu.Unlock()
		return
	}
	if info.activeProxies > 0 {
		info.activeProxies--
	}
	info.lastActivity = time.Now()
	active := info.activeProxies
	info.mu.Unlock()
	if active == 0 {
		m.rescheduleIdleTimer(runtimeKey, process, info)
	}
}

func (m *Manager) processInfo(runtimeKey, process string) *processInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processInfoLocked(runtimeKey, process)
}

func (m *Manager) processInfoLocked(runtimeKey, process string) *processInfo {
	procs, ok := m.processes[runtimeKey]
	if !ok {
		return nil
	}
	return procs[process]
}

func (m *Manager) consumePendingProxySessionsLocked(runtimeKey, process string) int {
	byProcess, ok := m.pendingProxySessions[runtimeKey]
	if !ok {
		return 0
	}
	count := byProcess[process]
	if count <= 0 {
		return 0
	}
	delete(byProcess, process)
	if len(byProcess) == 0 {
		delete(m.pendingProxySessions, runtimeKey)
	} else {
		m.pendingProxySessions[runtimeKey] = byProcess
	}
	return count
}

func (m *Manager) decrementPendingProxySessionLocked(runtimeKey, process string) {
	byProcess, ok := m.pendingProxySessions[runtimeKey]
	if !ok {
		return
	}
	count := byProcess[process]
	if count <= 1 {
		delete(byProcess, process)
	} else {
		byProcess[process] = count - 1
	}
	if len(byProcess) == 0 {
		delete(m.pendingProxySessions, runtimeKey)
		return
	}
	m.pendingProxySessions[runtimeKey] = byProcess
}

func (m *Manager) rescheduleIdleTimer(runtimeKey, process string, info *processInfo) {
	if info == nil {
		return
	}
	info.mu.Lock()
	if info.idleTimeout <= 0 || info.hasExited() {
		info.mu.Unlock()
		return
	}
	if info.activeProxies > 0 {
		if info.idleTimer != nil {
			info.idleTimer.Stop()
			info.idleTimer = nil
		}
		info.mu.Unlock()
		return
	}
	delay := info.idleTimeout - time.Since(info.lastActivity)
	if delay <= 0 {
		delay = 10 * time.Millisecond
	}
	if info.idleTimer != nil {
		info.idleTimer.Stop()
	}
	info.idleTimer = time.AfterFunc(delay, func() {
		m.stopIdleProcess(runtimeKey, process, info)
	})
	info.mu.Unlock()
}

func (m *Manager) stopIdleProcess(runtimeKey, process string, info *processInfo) {
	if info == nil {
		return
	}
	m.mu.Lock()
	procs, ok := m.processes[runtimeKey]
	if !ok || procs[process] != info {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	info.mu.Lock()
	if info.idleTimeout <= 0 || info.hasExited() {
		info.mu.Unlock()
		return
	}
	if info.activeProxies > 0 {
		info.mu.Unlock()
		m.rescheduleIdleTimer(runtimeKey, process, info)
		return
	}
	if time.Since(info.lastActivity) < info.idleTimeout {
		info.mu.Unlock()
		m.rescheduleIdleTimer(runtimeKey, process, info)
		return
	}
	info.mu.Unlock()

	stopManagedProcess(info)

	m.mu.Lock()
	defer m.mu.Unlock()
	procs, ok = m.processes[runtimeKey]
	if !ok {
		return
	}
	if procs[process] != info {
		return
	}
	delete(procs, process)
	if len(procs) == 0 {
		delete(m.processes, runtimeKey)
		m.unregisterWorktreeLocked(runtimeKey)
		return
	}
	m.processes[runtimeKey] = procs
}

func (m *Manager) Connect(slug, process string, conn net.Conn) error {
	if conn == nil {
		return errors.New("connection required")
	}
	_, _, err := m.EnsureProcessForTarget(slug, process)
	if err != nil {
		return err
	}

	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForSlugLocked(slug)
	if keyErr != nil {
		m.mu.Unlock()
		return keyErr
	}
	info := m.processes[runtimeKey][process]
	m.mu.Unlock()
	if info == nil || info.pty == nil {
		return errors.New("process not running")
	}

	subID := info.addSubscriber(conn)
	defer info.removeSubscriber(subID)

	_, err = io.Copy(info.pty, conn)
	return err
}

func buildRuntimeVars(cfg *config.ProjectConfig, project, envSlug, worktreePath, branch, apexZone string) map[string]string {
	dnsLabel := worktree.ProxyDNSLabelForSlug(cfg, envSlug)
	zone := strings.TrimPrefix(apexZone, ".")
	if zone == "" {
		zone = "localhost"
	}
	return map[string]string{
		"DEV_PROJECT":           project,
		"DEV_WORKTREE_SLUG":     envSlug,
		"DEV_WORKTREE_DNS_NAME": dnsLabel + "." + zone,
		"DEV_WORKTREE_PATH":     worktreePath,
		"DEV_WORKTREE_BRANCH":   branch,
	}
}

func buildTemplateVars(runtimeVars map[string]string, mainPath, worktreeState string) map[string]string {
	vars := procenv.CloneEnv(runtimeVars)
	vars["MAIN_WORKTREE"] = mainPath
	vars["WORKTREE_STATE"] = worktreeState
	vars["WORKTREE_PATH"] = runtimeVars["DEV_WORKTREE_PATH"]
	vars["WORKTREE_SLUG"] = runtimeVars["DEV_WORKTREE_SLUG"]
	vars["PROJECT_NAME"] = runtimeVars["DEV_PROJECT"]
	return vars
}

func (m *Manager) dependencyPortEnvVars(process string, needs *processNeeds, runtimeKey, mainRuntimeKey string, isMain bool) (map[string]string, error) {
	vars := map[string]string{}
	if needs == nil {
		return vars, nil
	}
	for _, dep := range needs.needs[process] {
		if err := needs.ensureExists(process, dep); err != nil {
			return nil, err
		}
		depRuntimeKey := runtimeKey
		if !isMain && needs.singleton[dep] {
			depRuntimeKey = mainRuntimeKey
		}
		port, ok := m.runningProcessPort(depRuntimeKey, dep)
		if !ok {
			continue
		}
		vars["DEV_PORT_"+envKey(dep)] = port
	}
	return vars, nil
}

func (m *Manager) runningProcessPort(slug, process string) (string, bool) {
	m.mu.Lock()
	info := m.processInfoLocked(slug, process)
	m.mu.Unlock()
	if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
		return "", false
	}
	return targetPort(info.network, info.address)
}

func resolveProcessTarget(proc map[string]any, name, worktreeState string) (network, address, port string, err error) {
	value, hasPort := proc["port"]
	if !hasPort {
		if _, hasProxy := proc["proxy"]; hasProxy {
			value = "random"
		} else {
			return "", "", "", nil
		}
	}
	switch typed := value.(type) {
	case int:
		return tcpTarget(typed)
	case int64:
		return tcpTarget(int(typed))
	case float64:
		return tcpTarget(int(typed))
	case string:
		mode := strings.ToLower(strings.TrimSpace(typed))
		switch mode {
		case "":
			return "", "", "", nil
		case "unix":
			return "unix", defaultSocketPath(worktreeState, name), "", nil
		case "random":
			allocated, err := allocateRandomPort()
			if err != nil {
				return "", "", "", err
			}
			return "tcp", fmt.Sprintf("127.0.0.1:%s", allocated), allocated, nil
		default:
			parsed, parseErr := strconv.Atoi(mode)
			if parseErr != nil {
				return "", "", "", fmt.Errorf("unsupported port value %q", typed)
			}
			return tcpTarget(parsed)
		}
	default:
		return "", "", "", fmt.Errorf("unsupported port type %T", value)
	}
}

func tcpTarget(port int) (network, address, portString string, err error) {
	if port <= 0 || port > 65535 {
		return "", "", "", fmt.Errorf("port out of range: %d", port)
	}
	portString = strconv.Itoa(port)
	return "tcp", fmt.Sprintf("127.0.0.1:%s", portString), portString, nil
}

func targetPort(network, address string) (string, bool) {
	if network != "tcp" || address == "" {
		return "", false
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(port) == "" {
		return "", false
	}
	return port, true
}

func expandVars(input string, vars map[string]string) string {
	out := input
	for key, value := range vars {
		if value == "" {
			continue
		}
		out = strings.ReplaceAll(out, "${"+key+"}", value)
	}
	return out
}

func envKey(name string) string {
	out := strings.ToUpper(name)
	out = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, out)
	return out
}

func defaultSocketPath(worktreeState, process string) string {
	return filepath.Join(worktreeState, process+".sock")
}

func resolveProcessIdleTimeout(proc map[string]any) time.Duration {
	if proc == nil {
		return 0
	}
	if _, proxied := proc["proxy"]; !proxied {
		return 0
	}
	if raw, ok := proc["idle_timeout"]; ok {
		timeout := parseProcessSeconds(raw)
		if timeout <= 0 {
			return 0
		}
		return timeout
	}
	return defaultProxyProcessIdleTimeout
}

func stopManagedProcess(info *processInfo) {
	if info == nil || info.cmd == nil || info.cmd.Process == nil {
		return
	}
	info.mu.Lock()
	if info.idleTimer != nil {
		info.idleTimer.Stop()
		info.idleTimer = nil
	}
	info.mu.Unlock()
	_ = interruptManagedProcess(info.cmd)

	select {
	case <-time.After(2 * time.Second):
		_ = killManagedProcess(info.cmd)
		<-info.exited
	case <-info.exited:
	}
}

func allocateRandomPort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate port: %w", err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", errors.New("unexpected listener addr type")
	}
	return fmt.Sprintf("%d", addr.Port), nil
}

func (p *processInfo) hasExited() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

func (p *processInfo) startOutputPump() {
	if p.pty == nil {
		return
	}
	go func() {
		reader := bufio.NewReader(p.pty)
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				if p.logFile != nil {
					p.mu.Lock()
					newSize, writeErr := appendOpenFileWithRetention(p.logFile, p.logSize, buf[:n], managedLogMaxBytes)
					if writeErr == nil {
						p.logSize = newSize
					}
					p.mu.Unlock()
				}
				p.broadcast(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}

func (p *processInfo) addSubscriber(w io.Writer) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	p.subs[p.nextID] = w
	return p.nextID
}

func (p *processInfo) removeSubscriber(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs, id)
}

func (p *processInfo) broadcast(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.subs {
		_, _ = w.Write(data)
	}
}

func runHook(command, wrapper, hookName, dir string, vars map[string]string) error {
	if command == "" {
		return nil
	}

	args, err := shellwords.Parse(command)
	if err != nil {
		return fmt.Errorf("parse hook: %w", err)
	}
	if len(args) == 0 {
		return nil
	}
	if strings.TrimSpace(wrapper) != "" {
		args, err = procenv.ApplyWrapper(wrapper, args)
		if err != nil {
			return fmt.Errorf("apply hook wrapper: %w", err)
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	hookVars := procenv.CloneEnv(vars)
	hookVars["DEV_HOOK_NAME"] = hookName
	cmd.Env = append(os.Environ(), procenv.FormatEnv(hookVars)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook failed: %w", err)
	}
	return nil
}

func resolveWorktreeBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Repos without commits do not have a resolvable HEAD; use a stable placeholder.
		if strings.Contains(string(output), "ambiguous argument 'HEAD'") {
			return "HEAD", nil
		}
		return "", fmt.Errorf("resolve worktree branch: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
