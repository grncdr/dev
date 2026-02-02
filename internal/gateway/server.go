package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"dev-mode/internal/config"
)

type Server struct {
	httpServer *http.Server
	listener   net.Listener
	store      *LeaseStore
	auth       config.UserGatewayAuth
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
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_agent/register", s.handleAgentRegister)
	mux.HandleFunc("/_agent/heartbeat", s.handleAgentHeartbeat)
	mux.HandleFunc("/_agent/unregister", s.handleAgentUnregister)
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

func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request) {
	label := s.extractLabel(r.Host)
	if label == "" || !s.store.hasActive(label) {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"code":  "gateway_label_unavailable",
			"error": "no active agent for label",
		})
		return
	}
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"code":  "gateway_forward_not_implemented",
		"error": "agent forwarding is not implemented yet",
	})
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
