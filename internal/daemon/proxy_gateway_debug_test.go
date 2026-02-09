package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	mgr.paths["main"] = base
	mgr.processes["main"] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}

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
	if !strings.Contains(bodyText, "https://main.public.example.com/ping") {
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
	if !strings.Contains(transcript, "redirect https://main.public.example.com/ping") {
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
	mgr.paths["main"] = base
	mgr.processes["main"] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}

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
	mgr.paths["main"] = base
	mgr.processes["main"] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: upstreamAddr,
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}

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
