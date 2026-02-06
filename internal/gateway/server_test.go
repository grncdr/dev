package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dev/internal/config"
)

type fakeDNSProvider struct {
	mu      sync.Mutex
	ensured []string
	removed []string
}

func (f *fakeDNSProvider) EnsureLabel(_ context.Context, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, label)
	return nil
}

func (f *fakeDNSProvider) RemoveLabel(_ context.Context, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, label)
	return nil
}

func TestServer_RegisterPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})

	body := bytes.NewBufferString(`{"project":"Foo Corp","slug":"main","label":"alpha","agent_id":"a1"}`)
	resp, err := client.Post("http://"+srv.Addr()+"/_agent/register", "application/json", body)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	srv2, client2 := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	}()

	resp, err = client2.Get("http://" + srv2.Addr() + "/_registry/labels")
	if err != nil {
		t.Fatalf("registry labels: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registry status: %d", resp.StatusCode)
	}
	var payload struct {
		Labels []Lease `json:"labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode labels: %v", err)
	}
	if len(payload.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(payload.Labels))
	}
	if payload.Labels[0].Status != LeasePending {
		t.Fatalf("expected pending label after restart, got %s", payload.Labels[0].Status)
	}
}

func TestServer_BasicAuthAppliesOnlyToPublicRequests(t *testing.T) {
	dir := t.TempDir()
	auth := config.DaemonGatewayAuth{Enabled: true, Username: "u", Password: "p"}
	srv, client := startGatewayServer(t, dir, auth)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	resp, err := client.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("public request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/_registry/labels", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("registry request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(b))
	}
}

func TestServer_ForwardsThroughAgentTunnel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok:" + r.URL.Path))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &Agent{
		GatewayURL:  "http://" + srv.Addr(),
		UpstreamURL: upstream.URL,
		Label:       "alpha",
		Project:     "Foo Corp",
		Slug:        "main",
		AgentID:     "agent-1",
	}
	go func() {
		_ = agent.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ping", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = "app.alpha.localhost"
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ok:/ping" {
				t.Fatalf("unexpected body: %q", string(body))
			}
			if resp.Header.Get("X-Upstream-Path") != "/ping" {
				t.Fatalf("missing upstream header")
			}
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarding never became ready; last error=%v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServer_ForwardsRedirectAndCookieHeadersWithoutFollowing(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "https://app.alpha.public.test/final")
			w.Header().Add("Set-Cookie", "session=abc; Domain=alpha.public.test; Path=/; HttpOnly")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &Agent{
		GatewayURL:  "http://" + srv.Addr(),
		UpstreamURL: upstream.URL,
		Label:       "alpha",
		Project:     "Foo Corp",
		Slug:        "main",
		AgentID:     "agent-1",
	}
	go func() {
		_ = agent.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ready", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = "alpha.localhost"
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarding never became ready; last error=%v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	noRedirectClient := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/redirect", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "app.alpha.localhost"
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("request through gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected status %d, got %d", http.StatusFound, resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "https://app.alpha.public.test/final" {
		t.Fatalf("expected location header to pass through, got %q", got)
	}
	if got := resp.Header.Get("Set-Cookie"); got != "session=abc; Domain=alpha.public.test; Path=/; HttpOnly" {
		t.Fatalf("expected Set-Cookie header to pass through, got %q", got)
	}
}

func TestServer_ForwardsConcurrentRequestsOverSingleTunnel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(75 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok:" + r.URL.Path))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &Agent{
		GatewayURL:  "http://" + srv.Addr(),
		UpstreamURL: upstream.URL,
		Label:       "alpha",
		Project:     "Foo Corp",
		Slug:        "main",
		AgentID:     "agent-1",
	}
	go func() {
		_ = agent.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ready", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = "alpha.localhost"
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusCreated {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarding never became ready; last error=%v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	paths := []string{"/a", "/b", "/c", "/d"}
	errCh := make(chan error, len(paths))
	start := make(chan struct{})
	for _, path := range paths {
		path := path
		go func() {
			<-start
			req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+path, nil)
			if err != nil {
				errCh <- err
				return
			}
			req.Host = "app.alpha.localhost"
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				errCh <- fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
				return
			}
			body, _ := io.ReadAll(resp.Body)
			if string(body) != "ok:"+path {
				errCh <- fmt.Errorf("unexpected body for %s: %q", path, string(body))
				return
			}
			errCh <- nil
		}()
	}
	close(start)
	for range paths {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent forward failed: %v", err)
		}
	}
}

func TestServer_ForwardsWebsocketUpgradeOverTunnel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		if !isUpgradeRequest(r) {
			http.Error(w, "expected upgrade request", http.StatusBadRequest)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = rw.WriteString("echo:" + line)
		_ = rw.Flush()
	}))
	defer upstream.Close()

	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &Agent{
		GatewayURL:  "http://" + srv.Addr(),
		UpstreamURL: upstream.URL,
		Label:       "alpha",
		Project:     "Foo Corp",
		Slug:        "main",
		AgentID:     "agent-1",
	}
	go func() {
		_ = agent.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ready", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = "alpha.localhost"
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusNoContent {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarding never became ready; last error=%v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer conn.Close()

	req := "GET /ws HTTP/1.1\r\n" +
		"Host: app.alpha.localhost\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: testtesttest=\r\n" +
		"\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}

	br := bufio.NewReader(conn)
	upgradeReq, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("new read response request: %v", err)
	}
	resp, err := http.ReadResponse(br, upgradeReq)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected 101, got %d body=%q", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write upgraded payload: %v", err)
	}
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read upgraded payload: %v", err)
	}
	if line != "echo:ping\n" {
		t.Fatalf("unexpected upgraded payload: %q", line)
	}
}

func TestServer_LogsPublicRequestMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	var logBuf bytes.Buffer
	srv, err := NewServer(ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    dir,
		DNSZone:    "tunnels.example.test",
		Auth:       config.DaemonGatewayAuth{},
		LogWriter:  &logBuf,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() { _ = srv.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	client := &http.Client{Timeout: 2 * time.Second}

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://" + srv.Addr() + "/_registry/labels")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &Agent{
		GatewayURL:  "http://" + srv.Addr(),
		UpstreamURL: upstream.URL,
		Label:       "alpha",
		Project:     "Foo Corp",
		Slug:        "main",
		AgentID:     "agent-1",
		Name:        "stephen",
	}
	go func() {
		_ = agent.Run(ctx)
	}()

	deadline = time.Now().Add(2 * time.Second)
	for {
		req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/ready", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = "alpha.localhost"
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusCreated {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("forwarding never became ready; last error=%v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/hello", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "app.alpha.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	lines := bytes.Split(bytes.TrimSpace(logBuf.Bytes()), []byte("\n"))
	if len(lines) == 0 {
		t.Fatalf("expected request logs")
	}
	var out map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &out); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if out["route_result"] != "forwarded" {
		t.Fatalf("expected forwarded route result, got %+v", out)
	}
	if out["label_extracted"] != "alpha" {
		t.Fatalf("expected label alpha, got %+v", out)
	}
	if out["project"] != "Foo Corp" || out["slug"] != "main" {
		t.Fatalf("expected project/slug metadata, got %+v", out)
	}
	if out["agent_id"] != "agent-1" || out["user"] != "stephen" {
		t.Fatalf("expected agent/user metadata, got %+v", out)
	}
}

func TestServer_PublicErrorsIncludeRequestID(t *testing.T) {
	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.DaemonGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "missing-label.localhost"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", resp.StatusCode)
	}
	hdrRequestID := strings.TrimSpace(resp.Header.Get("X-Request-Id"))
	if hdrRequestID == "" {
		t.Fatalf("missing X-Request-Id header")
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["request_id"] != hdrRequestID {
		t.Fatalf("request_id mismatch: header=%q body=%v", hdrRequestID, body["request_id"])
	}
}

func TestServer_SyncsDNSOnRegisterAndUnregister(t *testing.T) {
	dir := t.TempDir()
	fakeDNS := &fakeDNSProvider{}
	srv, err := NewServer(ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    dir,
		DNSZone:    "tunnels.example.test",
		Auth:       config.DaemonGatewayAuth{},
		DNS:        fakeDNS,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() { _ = srv.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	client := &http.Client{Timeout: 2 * time.Second}

	reqBody := bytes.NewBufferString(`{"project":"Foo Corp","slug":"main","label":"alpha","agent_id":"a1"}`)
	resp, err := client.Post("http://"+srv.Addr()+"/_agent/register", "application/json", reqBody)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var registerOut map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&registerOut); err != nil {
		resp.Body.Close()
		t.Fatalf("decode register body: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status: %d", resp.StatusCode)
	}
	if registerOut["public_hostname"] != "alpha.tunnels.example.test" {
		t.Fatalf("expected gateway-provided public hostname, got %+v", registerOut)
	}

	closeBody := bytes.NewBufferString(`{"label":"alpha"}`)
	resp, err = client.Post("http://"+srv.Addr()+"/_agent/unregister", "application/json", closeBody)
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status: %d", resp.StatusCode)
	}

	if len(fakeDNS.ensured) != 1 || fakeDNS.ensured[0] != "alpha" {
		t.Fatalf("expected ensure alpha, got %+v", fakeDNS.ensured)
	}
	if len(fakeDNS.removed) != 1 || fakeDNS.removed[0] != "alpha" {
		t.Fatalf("expected remove alpha, got %+v", fakeDNS.removed)
	}
}

func TestServer_IssuesCertFromInvite(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    dir,
		DNSZone:    "tunnels.example.test",
		Auth:       config.DaemonGatewayAuth{},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() { _ = srv.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	client := &http.Client{Timeout: 2 * time.Second}

	inviteReq := bytes.NewBufferString(`{"ttl_seconds":300,"uses":1}`)
	resp, err := client.Post("http://"+srv.Addr()+"/_admin/invites/create", "application/json", inviteReq)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	var inviteOut struct {
		InviteCode string `json:"invite_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inviteOut); err != nil {
		resp.Body.Close()
		t.Fatalf("decode invite: %v", err)
	}
	resp.Body.Close()
	if inviteOut.InviteCode == "" {
		t.Fatalf("expected invite code")
	}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	csrDER, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "tester"},
	}, key)
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	body, _ := json.Marshal(map[string]string{
		"invite_code": inviteOut.InviteCode,
		"name":        "tester",
		"csr":         string(csrPEM),
	})
	resp, err = client.Post("http://"+srv.Addr()+"/_agent/cert/issue", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("cert issue: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("cert issue status %d body=%s", resp.StatusCode, string(b))
	}
}

func TestEnsureAgentAuth_RequiresClientCertWhenEnabled(t *testing.T) {
	s := &Server{enforceAgentMTLS: true}
	req, err := http.NewRequest(http.MethodPost, "/_agent/register", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	rr := httptest.NewRecorder()
	if ok := s.ensureAgentAuth(rr, req); ok {
		t.Fatalf("expected auth to fail without mTLS")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}

	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{}}}
	rr = httptest.NewRecorder()
	if ok := s.ensureAgentAuth(rr, req); !ok {
		t.Fatalf("expected auth success with verified chains")
	}
}

func TestNewServer_RequiresDNSZone(t *testing.T) {
	_, err := NewServer(ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    t.TempDir(),
		Auth:       config.DaemonGatewayAuth{},
	})
	if err == nil {
		t.Fatalf("expected error when dns zone is empty")
	}
}

func startGatewayServer(t *testing.T, dataDir string, auth config.DaemonGatewayAuth) (*Server, *http.Client) {
	t.Helper()
	srv, err := NewServer(ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    dataDir,
		DNSZone:    "tunnels.example.test",
		Auth:       auth,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() {
		_ = srv.Serve()
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://" + srv.Addr() + "/_registry/labels")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return srv, client
}
