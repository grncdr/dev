package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dev-mode/internal/config"
)

type Server struct {
	httpServer *http.Server
	listener   net.Listener
	store      *LeaseStore
	auth       config.UserGatewayAuth
	tunnels    *tunnelPool
}

type RegisterRequest struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
	Label   string `json:"label"`
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type UnregisterRequest struct {
	Label string `json:"label"`
}

func NewServer(listenAddr, dataDir string, auth config.UserGatewayAuth) (*Server, error) {
	if listenAddr == "" {
		listenAddr = ":8080"
	}
	store, err := NewLeaseStore(dataDir)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("gateway listen %s: %w", listenAddr, err)
	}

	s := &Server{
		listener: ln,
		store:    store,
		auth:     auth,
		tunnels:  newTunnelPool(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_agent/register", s.handleAgentRegister)
	mux.HandleFunc("/_agent/heartbeat", s.handleAgentHeartbeat)
	mux.HandleFunc("/_agent/unregister", s.handleAgentUnregister)
	mux.HandleFunc("/_agent/tunnel/", s.handleAgentTunnel)
	mux.HandleFunc("/_registry/labels", s.handleRegistryLabels)
	mux.HandleFunc("/", s.handlePublic)

	s.httpServer = &http.Server{
		Handler:           s.withBasicAuth(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Serve() error {
	if s.httpServer == nil || s.listener == nil {
		return errors.New("gateway server not initialized")
	}
	return s.httpServer.Serve(s.listener)
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) withBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requiresBasicAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.auth.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(s.auth.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.auth.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="dev-mode gateway"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"code":  "gateway_auth_required",
				"error": "invalid gateway credentials",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requiresBasicAuth(path string) bool {
	if strings.HasPrefix(path, "/_agent/") {
		return false
	}
	if path == "/_registry/labels" {
		return false
	}
	return true
}

func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_body", "error": err.Error()})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_label", "error": "label is required"})
		return
	}
	now := time.Now().UTC()
	lease := Lease{
		Label:      req.Label,
		Project:    req.Project,
		Slug:       req.Slug,
		AgentID:    req.AgentID,
		Name:       req.Name,
		Status:     LeaseActive,
		CreatedAt:  now,
		LastSeenAt: now,
	}
	if err := s.store.upsert(lease); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "persist_failed", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req UnregisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_body", "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_label", "error": "label is required"})
		return
	}
	if err := s.store.touchActive(req.Label); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "label_not_found", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAgentUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req UnregisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_body", "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Label) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_label", "error": "label is required"})
		return
	}
	if err := s.store.revoke(req.Label); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "label_not_found", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRegistryLabels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"labels": s.store.list(),
	})
}

func (s *Server) handleAgentTunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		writeMethodNotAllowed(w)
		return
	}
	label := strings.TrimPrefix(r.URL.Path, "/_agent/tunnel/")
	label = strings.TrimSpace(label)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_label", "error": "label is required"})
		return
	}
	if !s.store.hasActive(label) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "label_not_found", "error": "label is not registered"})
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "hijack_unsupported", "error": "hijack not supported"})
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "hijack_failed", "error": err.Error()})
		return
	}
	if _, err := rw.WriteString("HTTP/1.1 200 OK\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return
	}
	s.tunnels.add(label, conn)
}

func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request) {
	label := s.extractLabel(r.Host)
	if label == "" || !s.store.hasActive(label) || !s.tunnels.has(label) {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"code":  "gateway_label_unavailable",
			"error": "no active agent for label",
		})
		return
	}
	if isUpgradeRequest(r) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"code":  "gateway_websocket_not_implemented",
			"error": "websocket forwarding is not implemented yet",
		})
		return
	}
	if err := s.forwardViaTunnel(label, w, r); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"code":  "gateway_upstream_error",
			"error": err.Error(),
		})
	}
}

func (s *Server) extractLabel(host string) string {
	host = stripPort(host)
	parts := strings.Split(host, ".")
	switch {
	case len(parts) == 0:
		return ""
	case len(parts) == 1:
		return parts[0]
	default:
		if s.store.hasActive(parts[0]) {
			return parts[0]
		}
		if len(parts) > 1 && s.store.hasActive(parts[1]) {
			return parts[1]
		}
		return parts[0]
	}
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func (s *Server) forwardViaTunnel(label string, w http.ResponseWriter, r *http.Request) error {
	tc := s.tunnels.acquire(label)
	if tc == nil {
		return errors.New("no tunnel connection available")
	}
	release := true
	defer func() {
		if release {
			s.tunnels.release(label, tc)
		}
	}()

	outReq := cloneRequestForTunnel(r)
	if err := outReq.Write(tc.bw); err != nil {
		release = false
		_ = tc.conn.Close()
		return err
	}
	if err := tc.bw.Flush(); err != nil {
		release = false
		_ = tc.conn.Close()
		return err
	}

	resp, err := http.ReadResponse(tc.br, outReq)
	if err != nil {
		release = false
		_ = tc.conn.Close()
		return err
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}

func cloneRequestForTunnel(in *http.Request) *http.Request {
	out := in.Clone(in.Context())
	out.URL = &url.URL{
		Path:     in.URL.Path,
		RawPath:  in.URL.RawPath,
		RawQuery: in.URL.RawQuery,
	}
	out.RequestURI = ""
	return out
}

func copyHeaders(dst, src http.Header) {
	for k := range dst {
		dst.Del(k)
	}
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func isUpgradeRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		r.Header.Get("Upgrade") != ""
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
		"code":  "method_not_allowed",
		"error": "method not allowed",
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("missing request body")
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}
