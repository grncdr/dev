package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dev/internal/daemon"
)

func TestRunDaemonStop(t *testing.T) {
	homeDir, err := os.MkdirTemp("/tmp", "dmhome")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Unsetenv("HOME")
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTP")
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTPS")
		_ = os.RemoveAll(homeDir)
	}()

	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		t.Fatalf("resolve socket: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	srv, err := daemon.NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	if err := waitForDaemon(socketPath, "", nil, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	if err := runDaemonStop(&Options{}); err != nil {
		t.Fatalf("runDaemonStop: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not shut down")
	}
}

func TestRunDaemonStopWhenNotRunning(t *testing.T) {
	homeDir, err := os.MkdirTemp("/tmp", "dmhome")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Unsetenv("HOME")
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTP")
		_ = os.Unsetenv("DEV_PROXY_LISTEN_HTTPS")
		_ = os.RemoveAll(homeDir)
	}()

	if err := runDaemonStop(&Options{}); err != nil {
		t.Fatalf("expected no error when daemon not running: %v", err)
	}
}
