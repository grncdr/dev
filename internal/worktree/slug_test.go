package worktree

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	t.Parallel()

	valid := []string{
		"feature-x",
		"my_branch",
		"v1",
		"a",
		"Feature123",
		"test-branch_v2",
		"has/slash",
		"feature/my-branch",
	}
	for _, slug := range valid {
		if err := ValidateSlug(slug); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", slug, err)
		}
	}

	invalid := []string{
		"",
		"-starts-with-dash",
		"_starts-with-underscore",
		"has space",
		"has..dots",
		"../traversal",
		"path/../attack",
		"semi;colon",
		"back`tick",
	}
	for _, slug := range invalid {
		if err := ValidateSlug(slug); err == nil {
			t.Errorf("expected %q to be invalid, but got no error", slug)
		}
	}
}

func TestParseWorktreeList(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	mainPath := filepath.Join(base, "foocorp")
	featurePath := filepath.Join(mainPath, "feature-x")

	input := fmt.Sprintf(`worktree %s
HEAD 123456
branch refs/heads/main

worktree %s
HEAD abcdef
branch refs/heads/feature-x
`, mainPath, featurePath)

	entries, err := parseWorktreeList(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[1].Path != featurePath {
		t.Fatalf("unexpected path: %s", entries[1].Path)
	}
	if BranchName(entries[1].Branch) != "feature-x" {
		t.Fatalf("unexpected branch: %q", entries[1].Branch)
	}

	slug, err := mainSlugFromEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != filepath.Base(mainPath) {
		t.Fatalf("expected main slug %q, got %q", filepath.Base(mainPath), slug)
	}
}

func TestMatchWorktree_PathBoundary(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Path: "/tmp/repo"},
		{Path: "/tmp/repo-feature"},
	}
	entry, err := matchWorktree(entries, "/tmp/repo-feature/subdir")
	if err != nil {
		t.Fatalf("matchWorktree: %v", err)
	}
	if entry.Path != "/tmp/repo-feature" {
		t.Fatalf("expected /tmp/repo-feature, got %s", entry.Path)
	}
}

func TestResolveSlugRequiresCWD(t *testing.T) {
	t.Parallel()

	if _, err := ResolveSlug(""); err == nil {
		t.Fatalf("expected ResolveSlug to fail when cwd is empty")
	}
}

func TestMatchWorktreeRequiresCWD(t *testing.T) {
	t.Parallel()

	if _, err := matchWorktree([]Entry{{Path: "/tmp/repo"}}, ""); err == nil {
		t.Fatalf("expected matchWorktree to fail when cwd is empty")
	}
}
