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

func TestServerHealthAndWorktree(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "devd.sock")
	if err := os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Unsetenv("DEV_MODE_PROXY_LISTEN_HTTP")
		_ = os.Unsetenv("DEV_MODE_PROXY_LISTEN_HTTPS")
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
