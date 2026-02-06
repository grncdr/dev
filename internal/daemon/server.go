package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"

	"dev/internal/config"
	"dev/internal/worktree"
)

type Server struct {
	socketPath   string
	listener     net.Listener
	httpServer   *http.Server
	manager      *Manager
	shutdown     chan struct{}
	once         sync.Once
	config       *config.ProjectConfig
	mainPath     string
	daemonConfig *config.DaemonConfig
	localDNSUDP  *mdns.Server
	localDNSTCP  *mdns.Server
	tunnelMu     sync.Mutex
	tunnels      map[string]*managedTunnel
}

func NewServer(socketPath string) (*Server, error) {
	if socketPath == "" {
		return nil, errors.New("socket path required")
	}

	if err := ensureSocketAvailable(socketPath); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	// Restrict socket permissions to owner only
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	mux := http.NewServeMux()
	s := &Server{
		socketPath: socketPath,
		listener:   ln,
		manager:    NewManager(),
		shutdown:   make(chan struct{}),
	}

	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/worktrees/start", s.handleWorktreeStart)
	mux.HandleFunc("/worktrees/stop", s.handleWorktreeStop)
	mux.HandleFunc("/worktrees/status", s.handleWorktreeStatus)
	mux.HandleFunc("/processes/start", s.handleProcessStart)
	mux.HandleFunc("/processes/stop", s.handleProcessStop)
	mux.HandleFunc("/processes/connect", s.handleProcessConnect)
	mux.HandleFunc("/tunnels/open", s.handleTunnelOpen)
	mux.HandleFunc("/tunnels/close", s.handleTunnelClose)
	mux.HandleFunc("/tunnels/status", s.handleTunnelsStatus)
	mux.HandleFunc("/shutdown", s.handleShutdown)

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s, nil
}

func ensureSocketAvailable(socketPath string) error {
	if _, err := os.Stat(socketPath); err != nil {
		return nil
	}

	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return errors.New("daemon already running (socket is active)")
	}

	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

func (s *Server) Serve() error {
	if err := s.loadConfig(); err != nil {
		return err
	}
	if err := s.startLocalDNS(); err != nil {
		return err
	}
	defer s.stopLocalDNS()
	if err := s.startProxy(); err != nil {
		return err
	}
	if err := s.restoreFromResume(); err != nil {
		logError(http.StatusInternalServerError, "resume_restore_failed", err)
	}
	defer os.Remove(s.socketPath)
	return s.httpServer.Serve(s.listener)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status: "ok",
		PID:    os.Getpid(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorktreeStart(w http.ResponseWriter, r *http.Request) {
	var req WorktreeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	if req.Slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_slug", errors.New("slug is required"))
		return
	}
	resp, err := s.manager.StartWorktreeFromDir(req.Slug, req.Path)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "start_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProcessStart(w http.ResponseWriter, r *http.Request) {
	var req WorktreeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	if req.Slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_slug", errors.New("slug is required"))
		return
	}
	resp, err := s.manager.StartProcessesFromDir(req.Slug, req.Path, req.Processes, req.All)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "start_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorktreeStop(w http.ResponseWriter, r *http.Request) {
	var req WorktreeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	if req.Slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_slug", errors.New("slug is required"))
		return
	}
	resp, err := s.manager.StopWorktreeFromDir(req.Slug, req.Path)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "stop_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProcessStop(w http.ResponseWriter, r *http.Request) {
	var req WorktreeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	if req.Slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_slug", errors.New("slug is required"))
		return
	}
	resp, err := s.manager.StopProcessesFromDir(req.Slug, req.Path, req.Processes, req.All)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "stop_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorktreeStatus(w http.ResponseWriter, r *http.Request) {
	var req WorktreeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	if req.Slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_slug", errors.New("slug is required"))
		return
	}
	resp, err := s.manager.StatusWorktreeFromDir(req.Slug, req.Path)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "status_failed", err)
		return
	}
	resp.Routing = s.routingStatusForWorktree(req.Slug, req.Path)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProcessConnect(w http.ResponseWriter, r *http.Request) {
	process := r.URL.Query().Get("process")
	slug := r.URL.Query().Get("slug")
	if process == "" || slug == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_params", errors.New("process and slug are required"))
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeErrorWithCode(w, http.StatusInternalServerError, "hijack_unsupported", errors.New("hijacking not supported"))
		return
	}

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		writeErrorWithCode(w, http.StatusInternalServerError, "hijack_failed", err)
		return
	}

	_, _ = buf.WriteString("HTTP/1.1 200 OK\r\n\r\n")
	_ = buf.Flush()

	go func() {
		_ = s.manager.Connect(slug, process, conn)
		_ = conn.Close()
	}()
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting_down"})
	go func() {
		s.persistAndStopRuntime()
		s.stopLocalDNS()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
		s.once.Do(func() { close(s.shutdown) })
	}()
}

func (s *Server) handleTunnelOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorWithCode(w, http.StatusMethodNotAllowed, "method_not_allowed", errors.New("method not allowed"))
		return
	}
	var req TunnelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	resp, err := s.openTunnel(req)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "tunnel_open_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTunnelClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorWithCode(w, http.StatusMethodNotAllowed, "method_not_allowed", errors.New("method not allowed"))
		return
	}
	var req TunnelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "invalid_body", err)
		return
	}
	resp, err := s.closeTunnel(req)
	if err != nil {
		writeErrorWithCode(w, http.StatusBadRequest, "tunnel_close_failed", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTunnelsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorWithCode(w, http.StatusMethodNotAllowed, "method_not_allowed", errors.New("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, s.tunnelsStatus())
}

func (s *Server) persistAndStopRuntime() {
	state := resumeState{
		Worktrees: s.runningWorktreesForResume(),
		Tunnels:   s.runningTunnelsForResume(),
	}
	if err := saveResumeState(state); err != nil {
		logError(http.StatusInternalServerError, "resume_save_failed", err)
	}
	s.stopAllTunnels()
	if s.manager != nil {
		s.manager.StopAllWorktrees()
	}
}

func (s *Server) restoreFromResume() error {
	state, err := loadResumeState()
	if err != nil {
		return err
	}
	if state == nil || (len(state.Worktrees) == 0 && len(state.Tunnels) == 0) {
		return nil
	}

	for _, wt := range state.Worktrees {
		if s.manager != nil && wt.Slug != "" {
			if _, err := s.manager.StartWorktreeFromDir(wt.Slug, wt.Path); err != nil {
				logError(http.StatusBadRequest, "resume_start_failed", err)
			}
		}
	}

	for _, req := range state.Tunnels {
		if strings.TrimSpace(req.Label) == "" || strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.GatewayURL) == "" {
			continue
		}
		if _, err := s.openTunnel(req); err != nil {
			logError(http.StatusBadRequest, "resume_tunnel_open_failed", err)
		}
	}

	return clearResumeState()
}

func (s *Server) runningWorktreesForResume() []resumeWorktree {
	if s.manager == nil {
		return nil
	}
	return s.manager.runningWorktrees()
}

func (s *Server) loadConfig() error {
	mainPath, err := worktree.ResolveMainPathInDir(".")
	if err != nil {
		return nil
	}
	cfgPath := filepath.Join(mainPath, config.DefaultProjectConfig)
	if _, err := os.Stat(cfgPath); err != nil {
		return nil // No project config, nothing to load
	}
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return err
	}
	s.config = cfg
	s.mainPath = mainPath
	// Skip loading user's daemon config during tests to avoid conflicts
	// with a locally running daemon.
	if !runningUnderGoTest() {
		daemonPath, err := config.ExpandUserPath(config.ResolveDaemonConfigPath())
		if err == nil {
			daemonCfg, _, err := config.LoadDaemonConfig(daemonPath)
			if err == nil {
				s.daemonConfig = daemonCfg
			}
		}
	}
	s.manager.SetApexZone(s.projectApexZone())
	s.manager.SetDaemonConfig(s.daemonConfig)
	return nil
}

func (s *Server) projectApexZone() string {
	if s.daemonConfig != nil {
		if apex := s.daemonConfig.LocalProxy.ApexZone; apex != "" {
			return apex
		}
	}
	return ".localhost"
}

func (s *Server) Wait() {
	<-s.shutdown
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeErrorWithCode(w, code, "bad_request", err)
}

func writeErrorWithCode(w http.ResponseWriter, code int, errCode string, err error) {
	logError(code, errCode, err)
	payload := map[string]string{
		"code":  errCode,
		"error": err.Error(),
	}
	writeJSON(w, code, payload)
}
