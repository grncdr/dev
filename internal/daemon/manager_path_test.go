package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"dev/internal/worktree"
)

func TestResolveWorktreePathPrefersDirHint(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "repo-a")
	repoB := filepath.Join(base, "repo-b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := runGit(repoA, "init"); err != nil {
		t.Fatalf("git init repoA: %v", err)
	}
	if err := runGit(repoB, "init"); err != nil {
		t.Fatalf("git init repoB: %v", err)
	}

	oldCwd, _ := os.Getwd()
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})
	if err := os.Chdir(repoB); err != nil {
		t.Fatal(err)
	}

	hint := filepath.Join(repoA, "nested")
	if err := os.MkdirAll(hint, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "main"
	m := NewManager()
	path, err := m.resolveWorktreePath(slug, "", hint)
	if err != nil {
		t.Fatalf("resolveWorktreePath: %v", err)
	}
	want, _ := filepath.EvalSymlinks(repoA)
	got, _ := filepath.EvalSymlinks(path)
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestStatusAndStopUseDirHint(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "repo-a")
	repoB := filepath.Join(base, "repo-b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoA, "init"); err != nil {
		t.Fatalf("git init repoA: %v", err)
	}
	if err := runGit(repoB, "init"); err != nil {
		t.Fatalf("git init repoB: %v", err)
	}

	cfg := `
[project]
name = "foocorp/monorepo"
`
	if err := os.WriteFile(filepath.Join(repoA, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	oldCwd, _ := os.Getwd()
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})
	if err := os.Chdir(repoB); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	hint := filepath.Join(repoA, "nested")
	if err := os.MkdirAll(hint, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "main"
	if _, err := m.StatusWorktreeFromDir(slug, hint); err != nil {
		t.Fatalf("status with hint: %v", err)
	}
	if _, err := m.StopWorktreeFromDir(slug, hint); err != nil {
		t.Fatalf("stop with hint: %v", err)
	}
}

func TestLookupRuntimeWorktreePathUsesRuntimeState(t *testing.T) {
	m := NewManager()
	projectName := "foocorp/monorepo"
	projectID, err := worktree.NormalizeIdentifierSegment(projectName)
	if err != nil {
		t.Fatalf("normalize project: %v", err)
	}
	path := filepath.Join(t.TempDir(), "repo")
	m.registerWorktreeLocked(runtimeKeyForPath(path), runtimeWorktree{
		Slug:    "main",
		Project: projectID,
		Path:    path,
	})

	got, ok := m.lookupRuntimeWorktreePath(projectName, "main")
	if !ok {
		t.Fatalf("expected runtime worktree path lookup to succeed")
	}
	if got != path {
		t.Fatalf("expected %s, got %s", path, got)
	}
}

func TestResolveWorktreePathPrefersRuntimeStateBeforePersistedState(t *testing.T) {
	m := NewManager()
	projectName := "foocorp/monorepo"
	projectID, err := worktree.NormalizeIdentifierSegment(projectName)
	if err != nil {
		t.Fatalf("normalize project: %v", err)
	}
	path := filepath.Join(t.TempDir(), "repo")
	m.registerWorktreeLocked(runtimeKeyForPath(path), runtimeWorktree{
		Slug:    "main",
		Project: projectID,
		Path:    path,
	})

	got, err := m.resolveWorktreePath("main", projectName, "")
	if err != nil {
		t.Fatalf("resolveWorktreePath: %v", err)
	}
	if got != path {
		t.Fatalf("expected %s, got %s", path, got)
	}
}

func TestResolveWorktreePathSupportsMainAliases(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfg := `
[project]
name = "foocorp/monorepo"
main_slug = "primary"
`
	if err := os.WriteFile(filepath.Join(repo, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	gotMain, err := m.resolveWorktreePath("main", "", repo)
	if err != nil {
		t.Fatalf("resolve main alias: %v", err)
	}
	want, _ := filepath.EvalSymlinks(repo)
	gotMainResolved, _ := filepath.EvalSymlinks(gotMain)
	if gotMainResolved != want {
		t.Fatalf("main alias resolved %s, want %s", gotMainResolved, want)
	}

	gotConfigured, err := m.resolveWorktreePath("primary", "", repo)
	if err != nil {
		t.Fatalf("resolve configured main alias: %v", err)
	}
	gotConfiguredResolved, _ := filepath.EvalSymlinks(gotConfigured)
	if gotConfiguredResolved != want {
		t.Fatalf("configured alias resolved %s, want %s", gotConfiguredResolved, want)
	}
}
