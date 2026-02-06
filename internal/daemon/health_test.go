package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dev/internal/config"
)

func TestEnsureProcessForTargetWaitsForHTTPHealth(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfg := `
[project]
name = "demo"

[process.web]
command = "sh -c \"sleep 1; python3 -m http.server ${PORT}\""
port = "random"
health = { type = "http", path = "/" }
`
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
	start := time.Now()
	network, address, err := m.EnsureProcessForTarget(slug, "web")
	if err != nil {
		t.Fatalf("ensure process target: %v", err)
	}
	if network != "tcp" {
		t.Fatalf("expected tcp network, got %s", network)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected readiness wait, got duration %s", time.Since(start))
	}
	resp, err := http.Get("http://" + address + "/")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}

	if _, err := m.StopWorktreeFromDir(slug, repoDir); err != nil {
		t.Fatalf("stop worktree: %v", err)
	}
}

func TestResolveProxyTargetAutoStartsAndWaitsForHealth(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfgText := `
[project]
name = "demo"

[proxy]
apex_zone = ".localhost"

[process.server]
command = "sh -c \"sleep 1; python3 -m http.server ${PORT}\""
port = "random"
health = { type = "http", path = "/" }

[[process.server.proxy]]
path = "/"
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfgText), 0o600); err != nil {
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

	cfg, _, err := config.LoadProjectConfig(filepath.Join(repoDir, ".dev.toml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	s := &Server{
		manager:  NewManager(),
		config:   cfg,
		mainPath: repoDir,
	}
	slug := defaultSlugForRepo(t, repoDir)
	start := time.Now()
	network, address, _, _, _, _, err := s.resolveProxyTarget(slug+".localhost", "/")
	if err != nil {
		t.Fatalf("resolve proxy target: %v", err)
	}
	if network != "tcp" {
		t.Fatalf("expected tcp network, got %s", network)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected health wait, got %s", time.Since(start))
	}
	resp, err := http.Get("http://" + address + "/")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	_ = resp.Body.Close()

	if _, err := s.manager.StopWorktreeFromDir(slug, repoDir); err != nil {
		t.Fatalf("stop worktree: %v", err)
	}
}

func TestEnsureProcessForTargetHonorsStartupTimeout(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfg := `
[project]
name = "demo"

[process.web]
command = "sh -c \"sleep 2; python3 -m http.server ${PORT}\""
port = "random"
health = { type = "http", path = "/" }
startup_timeout = 0.3
`
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
	_, _, err := m.EnsureProcessForTarget(slug, "web")
	if err == nil {
		t.Fatalf("expected startup timeout error")
	}
	if !strings.Contains(err.Error(), "health check timeout") {
		t.Fatalf("expected health timeout error, got %v", err)
	}
	_, _ = m.StopWorktreeFromDir(slug, repoDir)
}
