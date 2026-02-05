package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonRestoresWorktreesAfterCleanShutdown(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	configPath := filepath.Join(repoDir, ".dev-mode.toml")
	configBody := `
[project]
name = "demo"

[process.sleeper]
command = "sh -c \"sleep 60\""
port = "random"
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
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
	oldHTTP := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTP")
	oldHTTPS := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTPS")
	oldCwd, _ := os.Getwd()
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", oldHTTP)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", oldHTTPS)
		_ = os.Chdir(oldCwd)
	})
	if err := os.Setenv("HOME", baseDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", "off"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	socketPath := filepath.Join(baseDir, "devd.sock")
	if len(socketPath) > 80 {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("devd-resume-test-%d.sock", time.Now().UnixNano()))
		_ = os.Remove(socketPath)
	}
	slug := defaultSlugForRepo(t, repoDir)

	srv1, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	errCh1 := make(chan error, 1)
	go func() { errCh1 <- srv1.Serve() }()

	client1 := NewClient(socketPath)
	if err := waitForHealth(client1, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := client1.WorktreeStart(ctx, slug); err != nil {
		t.Fatalf("worktree start: %v", err)
	}
	if err := client1.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh1:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("server did not shut down")
	}

	resumePath, err := resumeStatePath()
	if err != nil {
		t.Fatalf("resume path: %v", err)
	}
	if _, err := os.Stat(resumePath); err != nil {
		t.Fatalf("expected resume state: %v", err)
	}

	srv2, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer restart: %v", err)
	}
	errCh2 := make(chan error, 1)
	go func() { errCh2 <- srv2.Serve() }()

	client2 := NewClient(socketPath)
	if err := waitForHealth(client2, 2*time.Second); err != nil {
		t.Fatalf("daemon restart not healthy: %v", err)
	}
	status, err := client2.WorktreeStatus(ctx, slug)
	if err != nil {
		t.Fatalf("status after restart: %v", err)
	}
	running := false
	for _, proc := range status.Processes {
		if proc.Name == "sleeper" && proc.Status == "running" {
			running = true
			break
		}
	}
	if !running {
		t.Fatalf("expected resumed running process, got %+v", status.Processes)
	}
	if _, err := os.Stat(resumePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected resume state cleared after restore")
	}

	if err := client2.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown 2: %v", err)
	}
	select {
	case err := <-errCh2:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("server did not shut down")
	}
}
