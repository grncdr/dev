package gateway

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dev/internal/config"
	"dev/internal/tunnelmux"
)

type Server struct {
	httpServer       *http.Server
	listener         net.Listener
	store            *LeaseStore
	invites          *InviteStore
	issuer           *CertIssuer
	dnsZone          string
	enforceAgentMTLS bool
	auth             config.DaemonGatewayAuth
	dns              DNSProvider
	certs            CertProvisioner
	tunnels          *tunnelPool
	logWriter        io.Writer
	logMu            sync.Mutex
	requestSeq       uint64
}

type ServerOptions struct {
	ListenAddr string
	DataDir    string
	DNSZone    string
	Auth       config.DaemonGatewayAuth
	DNS        DNSProvider
	Certs      CertProvisioner
	TLSConfig  *tls.Config
	LogWriter  io.Writer
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

type InviteCreateRequest struct {
	TTLSeconds int64 `json:"ttl_seconds"`
	Uses       int   `json:"uses"`
}

type CertIssueRequest struct {
	InviteCode string `json:"invite_code"`
	Name       string `json:"name"`
	CSR        string `json:"csr"`
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.ListenAddr == "" {
		opts.ListenAddr = ":8080"
	}
	dnsZone := normalizeHost(opts.DNSZone)
	if dnsZone == "" {
		return nil, errors.New("gateway dns zone is required")
	}
	store, err := NewLeaseStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	invites, err := NewInviteStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	issuer, err := NewCertIssuer(opts.DataDir)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("gateway listen %s: %w", opts.ListenAddr, err)
	}
	if opts.TLSConfig != nil {
		pool, err := issuer.ClientCAPool()
		if err != nil {
			return nil, err
		}
		opts.TLSConfig.ClientCAs = pool
		opts.TLSConfig.ClientAuth = tls.VerifyClientCertIfGiven
		ln = tls.NewListener(ln, opts.TLSConfig)
	}

	s := &Server{
		listener:         ln,
		store:            store,
		invites:          invites,
		issuer:           issuer,
		dnsZone:          dnsZone,
		enforceAgentMTLS: opts.TLSConfig != nil,
		auth:             opts.Auth,
		dns:              opts.DNS,
		certs:            opts.Certs,
		tunnels:          newTunnelPool(),
		logWriter:        opts.LogWriter,
	}
	if s.logWriter == nil {
		s.logWriter = os.Stdout
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/_agent/register", s.handleAgentRegister)
	mux.HandleFunc("/_agent/heartbeat", s.handleAgentHeartbeat)
	mux.HandleFunc("/_agent/unregister", s.handleAgentUnregister)
	mux.HandleFunc("/_agent/invites/create", s.handleAgentInviteCreate)
	mux.HandleFunc("/_agent/cert/issue", s.handleAgentCertIssue)
	mux.HandleFunc("/_agent/tunnel/", s.handleAgentTunnel)
	mux.HandleFunc("/_admin/invites/create", s.handleAdminInviteCreate)
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
			w.Header().Set("WWW-Authenticate", `Basic realm="dev gateway"`)
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
	if !s.ensureAgentAuth(w, r) {
		return
	}
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
	if s.dns != nil {
		if err := s.dns.EnsureLabel(r.Context(), req.Label); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "dns_sync_failed", "error": err.Error()})
			return
		}
	}
	if s.certs != nil {
		if err := s.certs.EnsureLabel(r.Context(), req.Label); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "acme_sync_failed", "error": err.Error()})
			return
		}
	}
	if err := s.store.upsert(lease); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "persist_failed", "error": err.Error()})
		return
	}
	resp := map[string]string{"status": "ok"}
	resp["public_host"] = s.dnsZone
	resp["public_hostname"] = req.Label + "." + s.dnsZone
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.ensureAgentAuth(w, r) {
		return
	}
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
	if !s.ensureAgentAuth(w, r) {
		return
	}
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
	if s.dns != nil {
		if err := s.dns.RemoveLabel(r.Context(), req.Label); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "dns_sync_failed", "error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAdminInviteCreate(w http.ResponseWriter, r *http.Request) {
	s.handleInviteCreate(w, r)
}

func (s *Server) handleAgentInviteCreate(w http.ResponseWriter, r *http.Request) {
	if !s.ensureAgentAuth(w, r) {
		return
	}
	s.handleInviteCreate(w, r)
}

func (s *Server) handleInviteCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req InviteCreateRequest
	if r.Body != nil {
		if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_body", "error": err.Error()})
			return
		}
	}
	ttl := 5 * time.Minute
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	uses := req.Uses
	if uses <= 0 {
		uses = 1
	}
	invite, err := s.invites.Create(ttl, uses)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "invite_create_failed", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"invite_code": invite.Code,
		"expires_at":  invite.ExpiresAt,
		"uses":        invite.UsesLeft,
	})
}

func (s *Server) handleAgentCertIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req CertIssueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_body", "error": err.Error()})
		return
	}
	req.InviteCode = strings.TrimSpace(req.InviteCode)
	if req.InviteCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "missing_invite_code", "error": "invite_code is required"})
		return
	}
	if req.Name == "" {
		req.Name = "dev-user"
	}
	if err := s.invites.Consume(req.InviteCode); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invite_invalid", "error": err.Error()})
		return
	}
	certPEM, caPEM, expiresAt, err := s.issuer.IssueClientCert([]byte(req.CSR), req.Name, 30*24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "cert_issue_failed", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cert_pem":   string(certPEM),
		"ca_pem":     string(caPEM),
		"expires_at": expiresAt,
	})
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
	if !s.ensureAgentAuth(w, r) {
		return
	}
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
	s.tunnels.set(label, tunnelmux.NewSession(conn))
}

func (s *Server) ensureAgentAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.enforceAgentMTLS {
		return true
	}
	if r == nil || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"code":  "agent_auth_required",
			"error": "mTLS client certificate is required",
		})
		return false
	}
	return true
}

func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := s.nextRequestID()
	w.Header().Set("X-Request-Id", requestID)

	logEntry := gatewayRequestLog{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		RequestID:   requestID,
		Method:      r.Method,
		Host:        r.Host,
		Path:        r.URL.Path,
		Status:      http.StatusBadGateway,
		RouteResult: "lease_inactive",
	}
	if r.URL.RawQuery != "" {
		logEntry.Path += "?" + r.URL.RawQuery
	}

	label := s.extractLabel(r.Host)
	logEntry.LabelExtracted = label
	lease, leaseFound := s.store.get(label)
	if label == "" {
		logEntry.RouteResult = "no_label"
		logEntry.ErrorCode = "gateway_label_unavailable"
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"code":       "gateway_label_unavailable",
			"error":      "no active agent for label",
			"request_id": requestID,
		})
		logEntry.Status = http.StatusBadGateway
		logEntry.LatencyMs = time.Since(start).Milliseconds()
		s.logRequest(logEntry)
		return
	}
	if !s.store.hasActive(label) {
		logEntry.RouteResult = "lease_inactive"
		logEntry.ErrorCode = "gateway_label_unavailable"
		if leaseFound {
			logEntry.Project = lease.Project
			logEntry.Slug = lease.Slug
			logEntry.AgentID = lease.AgentID
			logEntry.AgentName = lease.Name
			logEntry.User = lease.Name
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"code":       "gateway_label_unavailable",
			"error":      "no active agent for label",
			"request_id": requestID,
		})
		logEntry.Status = http.StatusBadGateway
		logEntry.LatencyMs = time.Since(start).Milliseconds()
		s.logRequest(logEntry)
		return
	}
	logEntry.Project = lease.Project
	logEntry.Slug = lease.Slug
	logEntry.AgentID = lease.AgentID
	logEntry.AgentName = lease.Name
	logEntry.User = lease.Name
	if !s.tunnels.has(label) {
		logEntry.RouteResult = "no_session"
		logEntry.ErrorCode = "gateway_label_unavailable"
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"code":       "gateway_label_unavailable",
			"error":      "no active agent for label",
			"request_id": requestID,
		})
		logEntry.Status = http.StatusBadGateway
		logEntry.LatencyMs = time.Since(start).Milliseconds()
		s.logRequest(logEntry)
		return
	}
	status, err := s.forwardViaTunnel(label, w, r)
	if err != nil {
		logEntry.RouteResult = "forward_error"
		logEntry.ErrorCode = "gateway_upstream_error"
		if status == 0 {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"code":       "gateway_upstream_error",
				"error":      err.Error(),
				"request_id": requestID,
			})
			logEntry.Status = http.StatusBadGateway
		} else {
			// Upstream headers were already forwarded, so we cannot emit a JSON error response now.
			logEntry.Status = status
		}
		logEntry.Error = err.Error()
		logEntry.LatencyMs = time.Since(start).Milliseconds()
		s.logRequest(logEntry)
		return
	}
	logEntry.RouteResult = "forwarded"
	logEntry.Status = status
	logEntry.LatencyMs = time.Since(start).Milliseconds()
	s.logRequest(logEntry)
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

func (s *Server) forwardViaTunnel(label string, w http.ResponseWriter, r *http.Request) (int, error) {
	sess := s.tunnels.get(label)
	if sess == nil {
		return 0, errors.New("no tunnel connection available")
	}
	stream, err := sess.OpenStream(r.Context())
	if err != nil {
		return 0, err
	}

	outReq := cloneRequestForTunnel(r)
	if err := outReq.Write(stream); err != nil {
		_ = stream.Close()
		return 0, err
	}
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, outReq)
	if err != nil {
		_ = stream.Close()
		return 0, err
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		return http.StatusSwitchingProtocols, s.forwardUpgradedResponse(w, stream, br, resp)
	}

	defer stream.Close()
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return resp.StatusCode, err
}

func (s *Server) forwardUpgradedResponse(w http.ResponseWriter, stream net.Conn, streamReader *bufio.Reader, resp *http.Response) error {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = stream.Close()
		return errors.New("hijack not supported for upgraded request")
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		_ = stream.Close()
		return err
	}
	if err := resp.Write(clientRW); err != nil {
		_ = clientConn.Close()
		_ = stream.Close()
		return err
	}
	if err := clientRW.Flush(); err != nil {
		_ = clientConn.Close()
		_ = stream.Close()
		return err
	}
	return proxyBidirectional(clientConn, clientRW, stream, streamReader)
}

type gatewayRequestLog struct {
	Timestamp      string `json:"ts"`
	RequestID      string `json:"request_id"`
	Method         string `json:"method"`
	Host           string `json:"host"`
	Path           string `json:"path"`
	Status         int    `json:"status"`
	LatencyMs      int64  `json:"latency_ms"`
	LabelExtracted string `json:"label_extracted,omitempty"`
	RouteResult    string `json:"route_result"`
	Project        string `json:"project,omitempty"`
	Slug           string `json:"slug,omitempty"`
	User           string `json:"user,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentName      string `json:"agent_name,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Error          string `json:"error,omitempty"`
}

func (s *Server) nextRequestID() string {
	n := atomic.AddUint64(&s.requestSeq, 1)
	return fmt.Sprintf("req-%d-%d", time.Now().UTC().UnixNano(), n)
}

func (s *Server) logRequest(entry gatewayRequestLog) {
	if s.logWriter == nil {
		return
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_, _ = s.logWriter.Write(append(payload, '\n'))
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
