package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonShutdownEndpoint(t *testing.T) {
	baseDir := t.TempDir()
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
		t.Fatalf("server did not shut down")
	}
}
