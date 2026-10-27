package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerSkipsDisabledLocalProxyWithoutProjectConfig(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy proxy address: %v", err)
	}
	defer occupied.Close()

	t.Setenv("DEV_PROXY_LISTEN_HTTP", occupied.Addr().String())
	t.Setenv("DEV_PROXY_LISTEN_HTTPS", "off")

	baseDir := t.TempDir()
	t.Setenv("HOME", baseDir)
	daemonConfigPath := filepath.Join(baseDir, "daemon.toml")
	if err := os.WriteFile(daemonConfigPath, []byte("[local-proxy]\nenabled = false\n"), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	t.Setenv("DEV_DAEMON_CONFIG", daemonConfigPath)
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(baseDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	socketPath := filepath.Join(baseDir, "devd.sock")
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := waitForHealth(client, 2*time.Second); err != nil {
		select {
		case serveErr := <-errCh:
			t.Fatalf("daemon stopped instead of skipping disabled local proxy: %v", serveErr)
		default:
		}
		t.Fatalf("daemon not healthy: %v", err)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestServerHealthAndWorktree(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "devd.sock")
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTP")
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTPS")
	}()

	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := client.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		t.Fatalf("server did not shut down")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	}
}
