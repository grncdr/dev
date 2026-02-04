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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/mattn/go-shellwords"

	"dev-mode/internal/config"
	"dev-mode/internal/procenv"
	"dev-mode/internal/worktree"
)

type Manager struct {
	mu        sync.Mutex
	processes map[string]map[string]*processInfo
	paths     map[string]string
	apexZone  string
}

func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]map[string]*processInfo),
		paths:     make(map[string]string),
		apexZone:  ".localhost",
	}
}

func (m *Manager) SetApexZone(zone string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apexZone = zone
}

type WorktreeStatus struct {
	Slug      string          `json:"slug"`
	Processes []ProcessStatus `json:"processes"`
}

type ProcessStatus struct {
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Status string `json:"status"`
}

type processInfo struct {
	cmd            *exec.Cmd
	network        string
	address        string
	health         *processHealthCheck
	startupTimeout time.Duration
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
	return m.startWorktreeFromDir(slug, "", nil, true)
}

func (m *Manager) StartWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	return m.startWorktreeFromDir(slug, dirHint, nil, true)
}

func (m *Manager) StartProcessesFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.startWorktreeFromDir(slug, dirHint, processes, all)
}

func (m *Manager) startWorktreeFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	path, err := resolveWorktreePath(slug, dirHint)
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
	branch, err := resolveWorktreeBranch(path)
	if err != nil {
		return nil, err
	}
	mainSlug := filepath.Base(mainPath)
	isMain := slug == mainSlug

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
	envSlug := effectiveWorktreeEnvSlug(cfg, slug, path, mainPath)
	runtimeVars := buildRuntimeVars(projectID, envSlug, path, branch, apexZone)
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
	for name, proc := range selected {
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
				fmt.Sprintf("DEV_MODE_PORT=%s", port),
				fmt.Sprintf("DEV_MODE_PORT_%s=%s", envKey(name), port),
			)
		}
		if network == "unix" && address != "" {
			cmd.Env = append(cmd.Env,
				fmt.Sprintf("DEV_MODE_SOCKET=%s", address),
				fmt.Sprintf("DEV_MODE_SOCKET_%s=%s", envKey(name), address),
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
			pty:            ptmx,
			logFile:        logFile,
			logSize:        logSize,
			exited:         make(chan struct{}),
			subs:           make(map[int]io.Writer),
		}
		info.startOutputPump()
		m.processes[slug][name] = info
		m.mu.Unlock()

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

	return &WorktreeStatus{Slug: slug, Processes: statuses}, nil
}

func resolveWorktreePath(slug, dirHint string) (string, error) {
	if dirHint != "" {
		if path, err := worktree.ResolvePathFromSlugInDir(slug, dirHint); err == nil {
			return path, nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return worktree.ResolvePathFromSlug(slug, cwd)
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

func (m *Manager) StopWorktree(slug string) (*WorktreeStatus, error) {
	return m.stopWorktreeFromDir(slug, "", nil, true)
}

func (m *Manager) StopWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	return m.stopWorktreeFromDir(slug, dirHint, nil, true)
}

func (m *Manager) StopProcessesFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	return m.stopWorktreeFromDir(slug, dirHint, processes, all)
}

func (m *Manager) stopWorktreeFromDir(slug, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	path, err := resolveWorktreePath(slug, dirHint)
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
	branch, err := resolveWorktreeBranch(path)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	apexZone := m.apexZone
	m.mu.Unlock()
	envSlug := effectiveWorktreeEnvSlug(cfg, slug, path, mainPath)
	runtimeVars := buildRuntimeVars(projectID, envSlug, path, branch, apexZone)

	if err := runHook(cfg.Hooks.PreStop, cfg.Commands.Wrapper, "pre_stop", path, runtimeVars); err != nil {
		return nil, err
	}

	m.mu.Lock()
	procs, ok := m.processes[slug]
	m.mu.Unlock()

	statuses := []ProcessStatus{}
	if !ok {
		return &WorktreeStatus{Slug: slug, Processes: statuses}, nil
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
		_ = info.cmd.Process.Signal(os.Interrupt)

		select {
		case <-time.After(2 * time.Second):
			_ = info.cmd.Process.Kill()
			<-info.exited
		case <-info.exited:
		}
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

	return &WorktreeStatus{Slug: slug, Processes: statuses}, nil
}

func (m *Manager) StatusWorktree(slug string) (*WorktreeStatus, error) {
	return m.StatusWorktreeFromDir(slug, "")
}

func (m *Manager) StatusWorktreeFromDir(slug, dirHint string) (*WorktreeStatus, error) {
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	if _, ok := m.WorktreePath(slug); !ok {
		if _, err := resolveWorktreePath(slug, dirHint); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	procs, ok := m.processes[slug]
	m.mu.Unlock()

	statuses := []ProcessStatus{}
	if !ok {
		return &WorktreeStatus{Slug: slug, Processes: statuses}, nil
	}

	for name, info := range procs {
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

	return &WorktreeStatus{Slug: slug, Processes: statuses}, nil
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
		"DEV_MODE_PROJECT":           project,
		"DEV_MODE_WORKTREE_SLUG":     envSlug,
		"DEV_MODE_WORKTREE_DNS_NAME": dnsLabel + "." + zone,
		"DEV_MODE_WORKTREE_PATH":     worktreePath,
		"DEV_MODE_WORKTREE_BRANCH":   branch,
	}
}

func effectiveWorktreeEnvSlug(cfg *config.ProjectConfig, slug, path, mainPath string) string {
	if cfg == nil || strings.TrimSpace(cfg.Project.MainSlug) == "" {
		return slug
	}
	if sameResolvedPath(path, mainPath) {
		return strings.ToLower(strings.TrimSpace(cfg.Project.MainSlug))
	}
	return slug
}

func buildTemplateVars(runtimeVars map[string]string, mainPath, worktreeState string) map[string]string {
	vars := procenv.CloneEnv(runtimeVars)
	vars["MAIN_WORKTREE"] = mainPath
	vars["WORKTREE_STATE"] = worktreeState
	vars["WORKTREE_PATH"] = runtimeVars["DEV_MODE_WORKTREE_PATH"]
	vars["WORKTREE_SLUG"] = runtimeVars["DEV_MODE_WORKTREE_SLUG"]
	vars["PROJECT_NAME"] = runtimeVars["DEV_MODE_PROJECT"]
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
	hookVars["DEV_MODE_HOOK_NAME"] = hookName
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
