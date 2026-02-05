package daemon

import (
	"os"
	"path/filepath"
	"testing"
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
command = "sh -c \"sleep 60\""

[process.api]
command = "sh -c \"sleep 60\""
needs = ["db"]
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev-mode.toml"), []byte(cfg), 0o600); err != nil {
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
command = "sh -c \"sleep 60\""

[process.server]
command = "sh -c \"sleep 60\""
needs = ["database"]
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev-mode.toml"), []byte(cfg), 0o600); err != nil {
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

	_, _ = m.StopWorktreeFromDir("feature", secondaryDir)
	_, _ = m.StopWorktreeFromDir(mainSlug, repoDir)
}
