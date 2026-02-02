package worktree

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
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

	slug, err := mainSlugFromEntries(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != filepath.Base(mainPath) {
		t.Fatalf("expected main slug %q, got %q", filepath.Base(mainPath), slug)
	}
}
