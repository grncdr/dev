package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dev/internal/agent"
	"dev/internal/config"
	"dev/internal/gateway"
)

func TestTunnelLifecycle(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(`
[project]
name = "demo"

[gateway]
url = "http://unused.local"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldCwd, _ := os.Getwd()
	oldProxyListenHTTP := os.Getenv("DEV_PROXY_LISTEN_HTTP")
	oldProxyListenHTTPS := os.Getenv("DEV_PROXY_LISTEN_HTTPS")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEV_PROXY_LISTEN_HTTP", oldProxyListenHTTP)
		_ = os.Setenv("DEV_PROXY_LISTEN_HTTPS", oldProxyListenHTTPS)
		_ = os.Chdir(oldCwd)
	})
	if err := os.Setenv("HOME", baseDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	gwDir := filepath.Join(baseDir, "gateway")
	gw, err := gateway.NewServer(gateway.ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    gwDir,
		DNSZone:    "public.example.dev",
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	go func() { _ = gw.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = gw.Shutdown(ctx)
	})

	socketPath := filepath.Join(baseDir, "devd.sock")
	if len(socketPath) > 80 {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("devd-tunnel-%d.sock", time.Now().UnixNano()))
	}
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	client := NewClient(socketPath)
	if err := waitForHealth(client, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	slug := defaultSlugForRepo(t, repoDir)
	_, err = client.TunnelOpen(ctx, TunnelRequest{
		Slug:       slug,
		Path:       repoDir,
		Label:      slug,
		GatewayURL: "http://" + gw.Addr(),
		Project:    "demo",
	})
	if err != nil {
		t.Fatalf("tunnel open: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer waitCancel()
	for {
		status, err := client.TunnelsStatus(waitCtx)
		if err == nil && len(status.Tunnels) == 1 && status.Tunnels[0].Status == "connected" {
			if status.Tunnels[0].PublicHost != "public.example.dev" {
				t.Fatalf("expected public host from gateway, got %+v", status.Tunnels[0])
			}
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("tunnel did not connect, last err=%v", err)
		default:
			time.Sleep(25 * time.Millisecond)
		}
	}

	if _, err := client.TunnelClose(ctx, TunnelRequest{Slug: slug}); err != nil {
		t.Fatalf("tunnel close: %v", err)
	}
	after, err := client.TunnelsStatus(ctx)
	if err != nil {
		t.Fatalf("tunnel status after close: %v", err)
	}
	if len(after.Tunnels) != 0 {
		t.Fatalf("expected no tunnels after close, got %+v", after.Tunnels)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("daemon serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("daemon did not stop")
	}
}

func TestOpenTunnel_RejectsIncompleteAuth(t *testing.T) {
	s := &Server{
		daemonConfig: &config.DaemonConfig{},
	}
	_, err := s.openTunnel(TunnelRequest{
		Slug:         "main",
		Label:        "alpha",
		GatewayURL:   "https://gw.example.test",
		AuthUsername: "alice",
	})
	if err == nil || !strings.Contains(err.Error(), "auth_username and auth_password") {
		t.Fatalf("expected auth validation error, got %v", err)
	}
}

func TestHandleTunnelAgentRequest_UsesRuntimeWorktreeBeforePersistedState(t *testing.T) {
	repoDir := t.TempDir()
	projectName := "tunnel-runtime-state-regression"
	cfgBody := `
[project]
name = "tunnel-runtime-state-regression"

[gateway]
expose = { app = { mode = "rewrite" } }

[process.app]
command = "app"
proxy = { path = "/" }
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.LoadProjectConfig(filepath.Join(repoDir, ".dev.toml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	mgr := NewManager()
	mgr.registerWorktreeLocked(runtimeKeyForPath(repoDir), runtimeWorktree{
		Slug:          "main",
		Project:       projectName,
		Path:          repoDir,
		DNSLabel:      "main",
		GatewayExpose: gatewayExposeRulesForConfig(cfg),
	})

	s := &Server{
		manager:      mgr,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
	}

	req := httptest.NewRequest(http.MethodGet, "https://main.public.example.dev/", nil)
	req.Host = "main.public.example.dev"
	tunnel := agent.TunnelStatus{
		Slug:          "main",
		Project:       projectName,
		Label:         "main",
		LocalBaseHost: "main.localhost",
		AuthUsername:  "alice",
		AuthPassword:  "secret",
	}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()

	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- s.handleTunnelAgentRequest(context.Background(), tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read tunneled response: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read tunneled body: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel agent request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", resp.StatusCode)
	}
}

func TestTunnelOpenImplicitlyRegistersProjectFromPath(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(`
[project]
name = "demo"

[gateway]
url = "http://unused.local"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldCwd, _ := os.Getwd()
	oldProxyListenHTTP := os.Getenv("DEV_PROXY_LISTEN_HTTP")
	oldProxyListenHTTPS := os.Getenv("DEV_PROXY_LISTEN_HTTPS")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEV_PROXY_LISTEN_HTTP", oldProxyListenHTTP)
		_ = os.Setenv("DEV_PROXY_LISTEN_HTTPS", oldProxyListenHTTPS)
		_ = os.Chdir(oldCwd)
	})
	if err := os.Setenv("HOME", baseDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(baseDir); err != nil {
		t.Fatal(err)
	}

	gwDir := filepath.Join(baseDir, "gateway")
	gw, err := gateway.NewServer(gateway.ServerOptions{
		ListenAddr: "127.0.0.1:0",
		DataDir:    gwDir,
		DNSZone:    "public.example.dev",
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	go func() { _ = gw.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = gw.Shutdown(ctx)
	})

	socketPath := filepath.Join(baseDir, "devd-implicit.sock")
	if len(socketPath) > 80 {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("devd-implicit-%d.sock", time.Now().UnixNano()))
	}
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.daemonConfig = &config.DaemonConfig{StateDir: filepath.Join(baseDir, "state")}
	srv.manager.SetDaemonConfig(srv.daemonConfig)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	client := NewClient(socketPath)
	if err := waitForHealth(client, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	slug := defaultSlugForRepo(t, repoDir)
	if _, err := client.TunnelOpen(ctx, TunnelRequest{
		Slug:       slug,
		Path:       repoDir,
		Label:      slug,
		GatewayURL: "http://" + gw.Addr(),
		Project:    "demo",
	}); err != nil {
		t.Fatalf("tunnel open: %v", err)
	}

	projectStatePath := filepath.Join(baseDir, "state", "worktree-projects.json")
	data, err := os.ReadFile(projectStatePath)
	if err != nil {
		t.Fatalf("read project state: %v", err)
	}
	if !strings.Contains(string(data), "\"main_path\"") {
		t.Fatalf("expected project state to contain main_path, got %s", string(data))
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("daemon serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("daemon did not stop")
	}
}
