package cli

import (
	"dev/internal/config"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dev/internal/daemon"
)

func TestRunDaemonStop(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("DEV_PROXY_LISTEN_HTTP", "off")
	t.Setenv("DEV_PROXY_LISTEN_HTTPS", "off")

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

	client := daemon.NewClient(socketPath)
	deadline := time.Now().Add(2 * time.Second)
	for !client.IsRunning() {
		if time.Now().After(deadline) {
			t.Fatal("daemon not healthy")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := runDaemonStop(&config.GlobalFlags{}); err != nil {
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
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEV_PROXY_LISTEN_HTTP", "off")
	t.Setenv("DEV_PROXY_LISTEN_HTTPS", "off")

	if err := runDaemonStop(&config.GlobalFlags{}); err != nil {
		t.Fatalf("expected no error when daemon not running: %v", err)
	}
}
