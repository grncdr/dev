package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProcessTargetsExplicit(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"proj:feature:rails",
		"proj:feature:webpack",
		"proj:other:*",
	}, nil)
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(targets))
	}
	feature := targets["feature"]
	if feature == nil || feature.all {
		t.Fatalf("expected specific processes for feature")
	}
	list := feature.processList()
	if len(list) != 2 || list[0] != "rails" || list[1] != "webpack" {
		t.Fatalf("unexpected process list: %+v", list)
	}
	other := targets["other"]
	if other == nil || !other.all {
		t.Fatalf("expected wildcard target for other")
	}
}

func TestResolveProcessTargetsWildcardWins(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"foo:rails",
		"foo:*",
		"foo:webpack",
	}, nil)
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	target := targets["foo"]
	if target == nil {
		t.Fatalf("expected target foo")
	}
	if !target.all {
		t.Fatalf("expected wildcard all=true")
	}
	if target.processList() != nil {
		t.Fatalf("expected no specific process list when wildcard is set")
	}
}

func TestResolveSlugProjectQualified(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev-mode.toml"), []byte("[project]\nname=\"demo\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	orig := rememberCWD()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	slug, err := resolveSlug(&Options{WorkingDir: repoDir}, "demo:feature/x")
	if err != nil {
		t.Fatalf("resolveSlug: %v", err)
	}
	if slug != "feature/x" {
		t.Fatalf("expected feature/x, got %q", slug)
	}
}
