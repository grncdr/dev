package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dev/internal/config"
)

func TestHandleProxyHTTPS_GatewayDebugLogWritesHTTPTranscript(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".dev.toml")
	cfgBody := `
[project]
name = "foocorp"

[gateway]
expose = { web = { mode = "rewrite", debug_log = "tmp/gateway-http.log" } }

[process.web]
proxy = { path = "/" }
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("redirect https://main.localhost/ping"))
	}))
	defer upstream.Close()

	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")
	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(base)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "main",
		Project:  "foocorp",
		Path:     base,
		DNSLabel: "main",
	})
	mgr.processes[runtimeKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager: mgr,
		daemonConfig: &config.DaemonConfig{
			LocalProxy: config.DaemonLocalProxyBlock{
				ApexZone: ".localhost",
				Allow:    "all",
			},
		},
		tunnels: map[string]*managedTunnel{
			"xyzz": {
				req: TunnelRequest{
					Slug:  "main",
					Label: "xyzz",
				},
				status: "connected",
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "https://xyzz.public.example.com/ping", strings.NewReader("hello from gateway"))
	req.Host = "xyzz.public.example.com"
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	s.handleProxyHTTPS(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	bodyText := string(body)
	if !strings.Contains(bodyText, "https://xyzz.public.example.com/ping") {
		t.Fatalf("expected rewritten response body, got %q", bodyText)
	}

	transcriptPath := filepath.Join(base, "tmp", "gateway-http.log")
	transcriptData, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	transcript := string(transcriptData)
	if !strings.Contains(transcript, "POST /ping HTTP/1.1") {
		t.Fatalf("expected request line in transcript, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "Host: xyzz.public.example.com") {
		t.Fatalf("expected incoming public host in transcript, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "hello from gateway") {
		t.Fatalf("expected request body in transcript, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "HTTP/1.1 200 OK") {
		t.Fatalf("expected response status line in transcript, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "redirect https://xyzz.public.example.com/ping") {
		t.Fatalf("expected rewritten response body in transcript, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, gatewayTranscriptExchangeDelimiterPrefix) {
		t.Fatalf("expected exchange delimiter prefix in transcript, got:\n%s", transcript)
	}
}

func TestHandleProxyHTTPS_GatewayDebugLogBinaryBodyPlaceholder(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".dev.toml")
	cfgBody := `
[project]
name = "foocorp"

[gateway]
expose = { web = { mode = "reverse_proxy", debug_log = "tmp/gateway-http.log" } }

[process.web]
proxy = { path = "/" }
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04})
	}))
	defer upstream.Close()

	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")
	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(base)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "main",
		Project:  "foocorp",
		Path:     base,
		DNSLabel: "main",
	})
	mgr.processes[runtimeKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager: mgr,
		daemonConfig: &config.DaemonConfig{
			LocalProxy: config.DaemonLocalProxyBlock{
				ApexZone: ".localhost",
				Allow:    "all",
			},
		},
		tunnels: map[string]*managedTunnel{
			"xyzz": {
				req: TunnelRequest{
					Slug:  "main",
					Label: "xyzz",
				},
				status: "connected",
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "https://xyzz.public.example.com/raw", strings.NewReader("\x00\x01\x02"))
	req.Host = "xyzz.public.example.com"
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	s.handleProxyHTTPS(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	transcriptPath := filepath.Join(base, "tmp", "gateway-http.log")
	transcriptData, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	transcript := string(transcriptData)
	if !strings.Contains(transcript, "[3 bytes binary data]") {
		t.Fatalf("expected binary placeholder for request body, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "[5 bytes binary data]") {
		t.Fatalf("expected binary placeholder for response body, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, gatewayTranscriptExchangeDelimiterPrefix) {
		t.Fatalf("expected exchange delimiter prefix in transcript, got:\n%s", transcript)
	}
}

func TestHandleProxyHTTPS_GatewayAuthRejectsUnauthorizedRequest(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".dev.toml")
	cfgBody := `
[project]
name = "foocorp"

[gateway]
expose = { web = { mode = "reverse_proxy" } }

[process.web]
proxy = { path = "/" }
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")
	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(base)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "main",
		Project:  "foocorp",
		Path:     base,
		DNSLabel: "main",
	})
	mgr.processes[runtimeKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager: mgr,
		daemonConfig: &config.DaemonConfig{
			LocalProxy: config.DaemonLocalProxyBlock{
				ApexZone: ".localhost",
				Allow:    "all",
			},
		},
		tunnels: map[string]*managedTunnel{
			"xyzz": {
				req: TunnelRequest{
					Slug:         "main",
					Label:        "xyzz",
					AuthUsername: "alice",
					AuthPassword: "secret",
				},
				status: "connected",
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "https://xyzz.public.example.com/ping", nil)
	req.Host = "xyzz.public.example.com"
	rec := httptest.NewRecorder()
	s.handleProxyHTTPS(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatalf("expected WWW-Authenticate header")
	}
}

func TestHandleProxyHTTPS_GatewayRewriteWithMainDNSOverride(t *testing.T) {
	base := t.TempDir()
	cfgPath := filepath.Join(base, ".dev.toml")
	cfgBody := `
[project]
name = "foocorp"

[local-dns]
overrides = { main = "foocorp" }

[gateway]
expose = { web = { mode = "rewrite" } }

[process.web]
proxy = { subdomain = "app", path = "/" }
`
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		mu             sync.Mutex
		upstreamHost   string
		upstreamOrigin string
		upstreamCookie string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamHost = r.Host
		upstreamOrigin = r.Header.Get("Origin")
		upstreamCookie = r.Header.Get("Cookie")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Location", "https://api.app.foocorp.localhost/ping")
		w.Header().Add("Set-Cookie", "session=abc; Domain=app.foocorp.localhost; Path=/; HttpOnly")
		_, _ = w.Write([]byte("redirect https://api.app.foocorp.localhost/ping"))
	}))
	defer upstream.Close()

	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")
	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(base)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "main",
		Project:  "foocorp",
		Path:     base,
		DNSLabel: "foocorp",
	})
	mgr.processes[runtimeKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager: mgr,
		daemonConfig: &config.DaemonConfig{
			LocalProxy: config.DaemonLocalProxyBlock{
				ApexZone: ".localhost",
				Allow:    "all",
			},
		},
		tunnels: map[string]*managedTunnel{
			"bobs-main-branch": {
				req: TunnelRequest{
					Slug:  "main",
					Label: "bobs-main-branch",
				},
				status: "connected",
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "https://app.bobs-main-branch.public.example.com/ping", nil)
	req.Host = "app.bobs-main-branch.public.example.com"
	req.Header.Set("Origin", "https://api.app.bobs-main-branch.public.example.com")
	req.Header.Set("Cookie", `$Version=1; sid=abc; $Domain=".api.app.bobs-main-branch.public.example.com"; $Path="/"`)
	rec := httptest.NewRecorder()
	s.handleProxyHTTPS(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	mu.Lock()
	gotUpstreamHost := upstreamHost
	gotUpstreamOrigin := upstreamOrigin
	gotUpstreamCookie := upstreamCookie
	mu.Unlock()
	if gotUpstreamHost != "app.foocorp.localhost" {
		t.Fatalf("expected upstream host app.foocorp.localhost, got %q", gotUpstreamHost)
	}
	if gotUpstreamOrigin != "https://api.app.foocorp.localhost" {
		t.Fatalf("expected rewritten upstream Origin, got %q", gotUpstreamOrigin)
	}
	if !strings.Contains(gotUpstreamCookie, `$Domain=".api.app.foocorp.localhost"`) {
		t.Fatalf("expected rewritten upstream cookie domain, got %q", gotUpstreamCookie)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if got := string(body); !strings.Contains(got, "https://api.app.bobs-main-branch.public.example.com/ping") {
		t.Fatalf("expected rewritten body host, got %q", got)
	}

	if got := resp.Header.Get("Location"); got != "https://api.app.bobs-main-branch.public.example.com/ping" {
		t.Fatalf("unexpected rewritten location: %q", got)
	}
	if got := resp.Header.Get("Set-Cookie"); got != "session=abc; Domain=app.bobs-main-branch.public.example.com; Path=/; HttpOnly" {
		t.Fatalf("unexpected rewritten cookie: %q", got)
	}
}
