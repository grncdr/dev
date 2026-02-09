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

func TestSharedProcessesOnlyInMainWorktree(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	configPath := filepath.Join(repoDir, ".dev.toml")
	configBody := `
[project]
name = "demo"

[process.shared]
singleton = true
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.worker]
singleton = false
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""
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

	secondaryDir := filepath.Join(baseDir, "repo-feature")
	if err := runGit(repoDir, "worktree", "add", secondaryDir, "-b", "feature"); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	oldHome := os.Getenv("HOME")
	oldProxyListenHTTP := os.Getenv("DEV_PROXY_LISTEN_HTTP")
	oldProxyListenHTTPS := os.Getenv("DEV_PROXY_LISTEN_HTTPS")
	oldCwd, _ := os.Getwd()
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

	socketPath := filepath.Join(baseDir, "devd.sock")
	if len(socketPath) > 80 {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("devd-shared-%d.sock", time.Now().UnixNano()))
	}
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	client := NewClient(socketPath)
	call := func(timeout time.Duration) (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), timeout)
	}

	if err := waitForHealth(client, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	secondarySlug := "feature"
	registerWorktreeForTest(t, nil, "demo", secondarySlug, secondaryDir, repoDir)
	ctx, cancel := call(4 * time.Second)
	resp, err := client.WorktreeStart(ctx, secondarySlug)
	cancel()
	if err != nil {
		t.Fatalf("worktree start secondary: %v", err)
	}
	if len(resp.Processes) != 1 {
		t.Fatalf("expected 1 process in secondary, got %+v", resp.Processes)
	}
	if resp.Processes[0].Name != "worker" {
		t.Fatalf("expected worker process, got %+v", resp.Processes)
	}

	mainSlug := defaultSlugForRepo(t, repoDir)
	ctx, cancel = call(4 * time.Second)
	respMain, err := client.WorktreeStart(ctx, mainSlug)
	cancel()
	if err != nil {
		t.Fatalf("worktree start main: %v", err)
	}
	if len(respMain.Processes) != 2 {
		t.Fatalf("expected 2 processes in main, got %+v", respMain.Processes)
	}
	seen := map[string]bool{}
	for _, proc := range respMain.Processes {
		seen[proc.Name] = true
	}
	if !seen["shared"] || !seen["worker"] {
		t.Fatalf("expected shared and worker processes, got %+v", respMain.Processes)
	}

	ctx, cancel = call(4 * time.Second)
	if _, err := client.WorktreeStop(ctx, secondarySlug); err != nil {
		cancel()
		t.Fatalf("stop secondary: %v", err)
	}
	cancel()
	ctx, cancel = call(8 * time.Second)
	if _, err := client.WorktreeStop(ctx, mainSlug); err != nil {
		cancel()
		t.Fatalf("stop main: %v", err)
	}
	cancel()

	ctx, cancel = call(5 * time.Second)
	if err := client.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("shutdown: %v", err)
	}
	cancel()

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
