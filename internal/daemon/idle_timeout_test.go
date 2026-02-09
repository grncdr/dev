package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveProcessIdleTimeout(t *testing.T) {
	tests := []struct {
		name string
		proc map[string]any
		want time.Duration
	}{
		{
			name: "non proxied process does not idle stop",
			proc: map[string]any{
				"command": "echo hi",
			},
			want: 0,
		},
		{
			name: "proxied process defaults to five minutes",
			proc: map[string]any{
				"command": "echo hi",
				"proxy":   []any{map[string]any{"path": "/"}},
			},
			want: 5 * time.Minute,
		},
		{
			name: "proxied process supports explicit timeout",
			proc: map[string]any{
				"command":      "echo hi",
				"proxy":        []any{map[string]any{"path": "/"}},
				"idle_timeout": 30,
			},
			want: 30 * time.Second,
		},
		{
			name: "zero idle timeout disables idle shutdown",
			proc: map[string]any{
				"command":      "echo hi",
				"proxy":        []any{map[string]any{"path": "/"}},
				"idle_timeout": 0,
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProcessIdleTimeout(tc.proc)
			if got != tc.want {
				t.Fatalf("resolveProcessIdleTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestIdleTimeoutStopsAndRestartsProxiedProcess(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Use the test binary itself as a TCP listener (see testmain_test.go).
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
`, testBin)
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
		t.Fatalf("start target: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	stopped := false
	for time.Now().Before(deadline) {
		status, err := m.StatusWorktreeFromDir(slug, repoDir)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if len(status.Processes) == 0 {
			stopped = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !stopped {
		t.Fatalf("expected idle timeout to stop process")
	}

	if _, _, err := m.EnsureProcessForTarget(slug, "web"); err != nil {
		t.Fatalf("restart target after idle stop: %v", err)
	}

	if _, err := m.StopWorktreeFromDir(slug, repoDir); err != nil {
		t.Fatalf("stop worktree: %v", err)
	}
}
