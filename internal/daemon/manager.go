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
	"dev/internal/worktree"
)

type Manager struct {
	mu                   sync.Mutex
	processes            map[string]map[string]*processInfo
	paths                map[string]string
	pendingProxySessions map[string]map[string]int
	apexZone             string
	daemonCfg            *config.DaemonConfig
}

const defaultProxyProcessIdleTimeout = 5 * time.Minute

func NewManager() *Manager {
	return &Manager{
		processes:            make(map[string]map[string]*processInfo),
		paths:                make(map[string]string),
		pendingProxySessions: make(map[string]map[string]int),
		apexZone:             ".localhost",
	}
}

func (m *Manager) SetApexZone(zone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apexZone = zone
}

func (m *Manager) SetDaemonConfig(cfg *config.DaemonConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.daemonCfg = cfg
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
	if err := m.ensureSlugPathCompatible(slug, path); err != nil {
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
	branch, err := resolveWorktreeBranch(path)
	if err != nil {
		return nil, err
	}
	isMain := sameResolvedPath(path, mainPath)

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
	runtimeVars := buildRuntimeVars(projectID, slug, path, branch, apexZone)
	templateVars := buildTemplateVars(runtimeVars, mainPath, worktreeState)
	if err := runHook(cfg.Hooks.PreStart, cfg.Commands.Wrapper, "pre_start", path, runtimeVars); err != nil {
		return nil, err
	}
	statuses := []ProcessStatus{}

	m.mu.Lock()
	if _, ok := m.processes[slug]; !ok {
		m.processes[slug] = make(map[string]*processInfo)
	}
	m.paths[slug] = path
	m.mu.Unlock()

	selected := selectProcesses(cfg.Processes, processes, all)
	needs, err := buildProcessNeeds(cfg.Processes)
	if err != nil {
		return nil, err
	}
	localSet, mainSet, err := needs.buildStartSets(selected, isMain)
	if err != nil {
		return nil, err
	}
	if !isMain && len(mainSet) > 0 {
		mainSlug, err := worktree.ResolveMainSlug(mainPath)
		if err != nil {
			return nil, fmt.Errorf("resolve main worktree slug: %w", err)
		}
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
		procVars := procenv.CloneEnv(templateVars)
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
		if existing := m.processes[slug][name]; existing != nil && existing.cmd != nil && existing.cmd.Process != nil && !existing.hasExited() {
			statuses = append(statuses, ProcessStatus{Name: name, PID: existing.cmd.Process.Pid, Status: "running"})
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()

		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(), procenv.FormatEnv(runtimeVars)...)
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
		info.activeProxies = m.consumePendingProxySessionsLocked(slug, name)
		info.startOutputPump()
		m.processes[slug][name] = info
		m.mu.Unlock()
		m.rescheduleIdleTimer(slug, name, info)

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

func (m *Manager) ensureSlugPathCompatible(slug, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.paths[slug]; ok && existing != "" && !sameResolvedPath(existing, path) {
		return fmt.Errorf("worktree slug %q is already associated with %s", slug, existing)
	}
	return nil
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
	if err := m.ensureSlugPathCompatible(slug, path); err != nil {
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
	runtimeVars := buildRuntimeVars(projectID, slug, path, branch, apexZone)

	if err := runHook(cfg.Hooks.PreStop, cfg.Commands.Wrapper, "pre_stop", path, runtimeVars); err != nil {
		return nil, err
	}

	m.mu.Lock()
	procs, ok := m.processes[slug]
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
	if existing, ok := m.processes[slug]; ok {
		for name := range selected {
			delete(existing, name)
		}
		if len(existing) == 0 {
			delete(m.processes, slug)
		} else {
			m.processes[slug] = existing
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
	if err := m.ensureSlugPathCompatible(slug, path); err != nil {
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

	if _, ok := m.WorktreePath(slug); !ok {
		m.mu.Lock()
		m.paths[slug] = path
		m.mu.Unlock()
	}

	m.mu.Lock()
	procs, ok := m.processes[slug]
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

func (m *Manager) TargetFor(slug, process string) (network string, address string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	procs, ok := m.processes[slug]
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
	m.mu.Lock()
	procs, ok := m.processes[slug]
	var info *processInfo
	if ok {
		info = procs[process]
	}
	m.mu.Unlock()
	if ok && info != nil && info.address != "" && info.network != "" && info.cmd != nil && info.cmd.Process != nil && !info.hasExited() {
		if err := waitForProcessReady(info); err != nil {
			return "", "", err
		}
		return info.network, info.address, nil
	}
	if ok && info != nil && info.cmd != nil && info.cmd.Process != nil && (info.address == "" || info.network == "") {
		return "", "", errors.New("process is running without proxy target; set port = \"unix\", \"random\", or an integer")
	}
	dirHint, _ := m.WorktreePath(slug)
	status, err := m.StartWorktreeFromDir(slug, dirHint)
	if err != nil {
		return "", "", err
	}
	for _, proc := range status.Processes {
		if proc.Name == process {
			network, address, err := m.TargetFor(slug, process)
			if err != nil {
				return "", "", err
			}
			m.mu.Lock()
			current := m.processes[slug][process]
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
	m.mu.Lock()
	defer m.mu.Unlock()
	path, ok := m.paths[slug]
	return path, ok && path != ""
}

func (m *Manager) StopAllWorktrees() {
	entries := m.runningWorktrees()
	for _, entry := range entries {
		_, _ = m.StopWorktreeFromDir(entry.Slug, entry.Path)
	}
}

func (m *Manager) beginProxySession(slug, process string) {
	m.mu.Lock()
	info := m.processInfoLocked(slug, process)
	if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
		if _, ok := m.pendingProxySessions[slug]; !ok {
			m.pendingProxySessions[slug] = make(map[string]int)
		}
		m.pendingProxySessions[slug][process]++
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
	m.mu.Lock()
	info := m.processInfoLocked(slug, process)
	if info == nil || info.cmd == nil || info.cmd.Process == nil || info.hasExited() {
		m.decrementPendingProxySessionLocked(slug, process)
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
		m.rescheduleIdleTimer(slug, process, info)
	}
}

func (m *Manager) processInfo(slug, process string) *processInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processInfoLocked(slug, process)
}

func (m *Manager) processInfoLocked(slug, process string) *processInfo {
	procs, ok := m.processes[slug]
	if !ok {
		return nil
	}
	return procs[process]
}

func (m *Manager) consumePendingProxySessionsLocked(slug, process string) int {
	byProcess, ok := m.pendingProxySessions[slug]
	if !ok {
		return 0
	}
	count := byProcess[process]
	if count <= 0 {
		return 0
	}
	delete(byProcess, process)
	if len(byProcess) == 0 {
		delete(m.pendingProxySessions, slug)
	} else {
		m.pendingProxySessions[slug] = byProcess
	}
	return count
}

func (m *Manager) decrementPendingProxySessionLocked(slug, process string) {
	byProcess, ok := m.pendingProxySessions[slug]
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
		delete(m.pendingProxySessions, slug)
		return
	}
	m.pendingProxySessions[slug] = byProcess
}

func (m *Manager) rescheduleIdleTimer(slug, process string, info *processInfo) {
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
		m.stopIdleProcess(slug, process, info)
	})
	info.mu.Unlock()
}

func (m *Manager) stopIdleProcess(slug, process string, info *processInfo) {
	if info == nil {
		return
	}
	m.mu.Lock()
	procs, ok := m.processes[slug]
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
		m.rescheduleIdleTimer(slug, process, info)
		return
	}
	if time.Since(info.lastActivity) < info.idleTimeout {
		info.mu.Unlock()
		m.rescheduleIdleTimer(slug, process, info)
		return
	}
	info.mu.Unlock()

	stopManagedProcess(info)

	m.mu.Lock()
	defer m.mu.Unlock()
	procs, ok = m.processes[slug]
	if !ok {
		return
	}
	if procs[process] != info {
		return
	}
	delete(procs, process)
	if len(procs) == 0 {
		delete(m.processes, slug)
		return
	}
	m.processes[slug] = procs
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
	info := m.processes[slug][process]
	m.mu.Unlock()
	if info == nil || info.pty == nil {
		return errors.New("process not running")
	}

	subID := info.addSubscriber(conn)
	defer info.removeSubscriber(subID)

	_, err = io.Copy(info.pty, conn)
	return err
}

func buildRuntimeVars(project, envSlug, worktreePath, branch, apexZone string) map[string]string {
	dnsLabel := worktree.SlugDNSLabel(envSlug)
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
