package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdleFollowStopsAndWakesDependents(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	testBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	cfg := fmt.Sprintf(`
[project]
name = "demo"

[process.web]
command = "%s -test.run=^$ -test.timeout=30s"
port = "random"
health = { type = "tcp" }
idle_timeout = 1.0

[process.web.env]
DEV_TEST_TCP_LISTEN = "1"

[[process.web.proxy]]
path = "/"

[process.worker]
command = "%s -test.run=^$ -test.timeout=30s"
idle_follow = ["web"]

[process.worker.env]
DEV_TEST_SIGNAL_HELPER = "child"
`, testBin, testBin)

	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
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
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Chdir(oldCwd)
	})
	_ = os.Setenv("HOME", baseDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)

	// Start both processes.
	if _, _, err := m.EnsureProcessForTarget(slug, "web"); err != nil {
		t.Fatalf("start web: %v", err)
	}
	if _, err := m.StartProcessesFromDir(slug, repoDir, []string{"worker"}, false); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	// Wait for idle timeout to stop web and propagate to worker.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		status, err := m.StatusWorktreeFromDir(slug, repoDir)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if len(status.Processes) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	status, err := m.StatusWorktreeFromDir(slug, repoDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Processes) != 0 {
		t.Fatalf("expected both processes to be stopped by idle follow, got %d", len(status.Processes))
	}

	// Auto-start web due to demand, expect worker to wake as well.
	if _, _, err := m.EnsureProcessForTarget(slug, "web"); err != nil {
		t.Fatalf("restart web: %v", err)
	}

	wakeDeadline := time.Now().Add(4 * time.Second)
	workerUp := false
	for time.Now().Before(wakeDeadline) {
		status, err := m.StatusWorktreeFromDir(slug, repoDir)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, p := range status.Processes {
			if p.Name == "worker" && p.Status == "running" {
				workerUp = true
				break
			}
		}
		if workerUp {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !workerUp {
		t.Fatalf("expected worker to auto-start after web woke from idle")
	}

	if _, err := m.StopWorktreeFromDir(slug, repoDir); err != nil {
		t.Fatalf("stop worktree: %v", err)
	}
}

func TestIdleFollowDoesNotWakeDependentsWhenLeaderAlreadyRunning(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	testBin, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	cfg := fmt.Sprintf(`
[project]
name = "demo"

[process.web]
command = "%s -test.run=^$ -test.timeout=30s"
port = "random"
health = { type = "tcp" }

[process.web.env]
DEV_TEST_TCP_LISTEN = "1"

[[process.web.proxy]]
path = "/"

[process.worker]
command = "%s -test.run=^$ -test.timeout=30s"
idle_follow = ["web"]

[process.worker.env]
DEV_TEST_SIGNAL_HELPER = "child"
`, testBin, testBin)

	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
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
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Chdir(oldCwd)
	})
	_ = os.Setenv("HOME", baseDir)
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)

	if _, _, err := m.EnsureProcessForTarget(slug, "web"); err != nil {
		t.Fatalf("start web: %v", err)
	}

	if _, err := m.StopProcessesFromDir(slug, repoDir, []string{"worker"}, false); err != nil {
		t.Fatalf("stop worker: %v", err)
	}

	m.mu.Lock()
	runtimeKey, keyErr := m.runtimeKeyForTargetLocked(slug, repoDir)
	if keyErr != nil {
		m.mu.Unlock()
		t.Fatalf("runtime key: %v", keyErr)
	}
	if _, ok := m.idleFollowStopped[runtimeKey]; !ok {
		m.idleFollowStopped[runtimeKey] = make(map[string]bool)
	}
	m.idleFollowStopped[runtimeKey]["worker"] = true
	m.mu.Unlock()

	if _, _, err := m.EnsureProcessForTarget(slug, "web"); err != nil {
		t.Fatalf("ensure web: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	status, err := m.StatusWorktreeFromDir(slug, repoDir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	webRunning := false
	for _, proc := range status.Processes {
		if proc.Name == "web" && proc.Status == "running" {
			webRunning = true
		}
		if proc.Name == "worker" && proc.Status == "running" {
			t.Fatalf("expected worker to remain stopped when web was already running")
		}
	}
	if !webRunning {
		t.Fatalf("expected web to keep running")
	}

	if _, err := m.StopWorktreeFromDir(slug, repoDir); err != nil {
		t.Fatalf("stop worktree: %v", err)
	}
}
