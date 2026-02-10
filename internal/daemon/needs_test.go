package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessNeedsStartsDependencies(t *testing.T) {
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

[process.db]
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.api]
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""
needs = ["db"]
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
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	status, err := m.StartProcessesFromDir(slug, repoDir, []string{"api"}, false)
	if err != nil {
		t.Fatalf("start processes: %v", err)
	}
	seen := map[string]bool{}
	for _, proc := range status.Processes {
		seen[proc.Name] = true
	}
	if !seen["api"] || !seen["db"] {
		t.Fatalf("expected api and db to start, got %+v", status.Processes)
	}
	_, _ = m.StopWorktreeFromDir(slug, repoDir)
}

func TestProcessNeedsStartsSingletonInMain(t *testing.T) {
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

[process.database]
singleton = true
port = 15432
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.server]
port = 18080
command = "sh -c \"echo ${DEV_PORT_DATABASE} > dep_port.txt; trap 'exit 0' INT TERM; while :; do sleep 1; done\""
needs = ["database"]
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

	secondaryDir := filepath.Join(baseDir, "repo-feature")
	if err := runGit(repoDir, "worktree", "add", secondaryDir, "-b", "feature"); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	registerWorktreeForTest(t, nil, "demo", "feature", secondaryDir, repoDir)
	status, err := m.StartWorktreeFromDir("feature", secondaryDir)
	if err != nil {
		t.Fatalf("start feature worktree: %v", err)
	}
	if len(status.Processes) != 1 || status.Processes[0].Name != "server" {
		t.Fatalf("expected only server in feature, got %+v", status.Processes)
	}

	mainSlug := defaultSlugForRepo(t, repoDir)
	mainStatus, err := m.StatusWorktreeFromDir(mainSlug, repoDir)
	if err != nil {
		t.Fatalf("main status: %v", err)
	}
	seen := map[string]bool{}
	for _, proc := range mainStatus.Processes {
		seen[proc.Name] = true
	}
	if !seen["database"] {
		t.Fatalf("expected database in main, got %+v", mainStatus.Processes)
	}
	if seen["server"] {
		t.Fatalf("did not expect server in main, got %+v", mainStatus.Processes)
	}
	depPortPath := filepath.Join(secondaryDir, "dep_port.txt")
	timeout := time.After(4 * time.Second)
	for {
		data, readErr := os.ReadFile(depPortPath)
		if readErr == nil {
			got := strings.TrimSpace(string(data))
			if got != "15432" {
				t.Fatalf("expected DEV_PORT_DATABASE=15432, got %q", got)
			}
			break
		}
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for dependency port file: %v", readErr)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	_, _ = m.StopWorktreeFromDir("feature", secondaryDir)
	_, _ = m.StopWorktreeFromDir(mainSlug, repoDir)
}

func TestProcessNeedsInjectsOnlyDependencyPortVars(t *testing.T) {
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

[process.db]
port = 15432
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.cache]
port = 16379
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.api]
port = 18080
command = "sh -c \"echo ${DEV_PORT_DB},${DEV_PORT_CACHE},${DEV_PORT_API},${PORT} > dep_ports.txt; trap 'exit 0' INT TERM; while :; do sleep 1; done\""
needs = ["db"]
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
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	status, err := m.StartProcessesFromDir(slug, repoDir, []string{"api"}, false)
	if err != nil {
		t.Fatalf("start api process: %v", err)
	}
	seen := map[string]bool{}
	for _, proc := range status.Processes {
		seen[proc.Name] = true
	}
	if !seen["api"] || !seen["db"] {
		t.Fatalf("expected api and db to start, got %+v", status.Processes)
	}
	if seen["cache"] {
		t.Fatalf("did not expect cache to start, got %+v", status.Processes)
	}

	depPortsPath := filepath.Join(repoDir, "dep_ports.txt")
	timeout := time.After(4 * time.Second)
	for {
		data, readErr := os.ReadFile(depPortsPath)
		if readErr == nil {
			got := strings.TrimSpace(string(data))
			if got != "15432,,18080,18080" {
				t.Fatalf("expected dependency-only port vars, got %q", got)
			}
			break
		}
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for dependency port file: %v", readErr)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	_, _ = m.StopWorktreeFromDir(slug, repoDir)
}
