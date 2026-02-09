package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

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
		Label:      slug,
		GatewayURL: "http://" + gw.Addr(),
		Project:    "demo",
		Upstream:   upstream.URL,
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
		Upstream:     "http://127.0.0.1:9999",
		AuthUsername: "alice",
	})
	if err == nil || !strings.Contains(err.Error(), "auth_username and auth_password") {
		t.Fatalf("expected auth validation error, got %v", err)
	}
}
