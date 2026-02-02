package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorktreeStartStopEndToEnd(t *testing.T) {
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

[processes.sleeper]
singleton = false
command = "sh -c \"echo ${SLEEPER_FLAG}; sleep 60\""
port = "unix"

[processes.sleeper.env]
SLEEPER_FLAG = "enabled"

[hooks]
pre_start = "sh -c \"echo pre_start > hook_pre_start.txt\""
post_start = "sh -c \"echo post_start > hook_post_start.txt\""
pre_stop = "sh -c \"echo pre_stop > hook_pre_stop.txt\""
post_stop = "sh -c \"echo post_stop > hook_post_stop.txt\""
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldProxyListenHTTP := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTP")
	oldProxyListenHTTPS := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTPS")
	oldCwd, _ := os.Getwd()
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", oldProxyListenHTTP)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", oldProxyListenHTTPS)
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
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	client := NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	if err := waitForHealth(client, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	slug := filepath.Base(repoDir)
	start, err := client.WorktreeStart(ctx, slug)
	if err != nil {
		t.Fatalf("worktree start: %v", err)
	}
	if len(start.Processes) != 1 || start.Processes[0].Status != "running" {
		t.Fatalf("expected running process, got %+v", start.Processes)
	}

	logPath := filepath.Join(baseDir, ".local", "state", "dev-mode", "demo", "worktrees", slug, "sleeper.log")
	if err := waitForLogContains(logPath, "enabled", 2*time.Second); err != nil {
		t.Fatalf("expected env var in log: %v", err)
	}

	status, err := client.WorktreeStatus(ctx, slug)
	if err != nil {
		t.Fatalf("worktree status: %v", err)
	}
	if len(status.Processes) != 1 || status.Processes[0].PID == 0 {
		t.Fatalf("expected running pid, got %+v", status.Processes)
	}

	stop, err := client.WorktreeStop(ctx, slug)
	if err != nil {
		t.Fatalf("worktree stop: %v", err)
	}
	if len(stop.Processes) != 1 || stop.Processes[0].Status != "stopped" {
		t.Fatalf("expected stopped process, got %+v", stop.Processes)
	}

	for _, name := range []string{
		"hook_pre_start.txt",
		"hook_post_start.txt",
		"hook_pre_stop.txt",
		"hook_post_stop.txt",
	} {
		path := filepath.Join(repoDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected hook file %s to exist: %v", name, err)
		}
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

func waitForHealth(client *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, err := client.Health(ctx)
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("health check timeout")
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dev-mode",
		"GIT_AUTHOR_EMAIL=dev-mode@example.com",
		"GIT_COMMITTER_NAME=dev-mode",
		"GIT_COMMITTER_EMAIL=dev-mode@example.com",
	)
	return cmd.Run()
}

func waitForLogContains(path, needle string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), needle) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("log did not contain expected value")
}
