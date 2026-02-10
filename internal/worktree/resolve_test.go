package worktree

import "testing"

func TestResolvePathFromSlugRequiresCWD(t *testing.T) {
	t.Parallel()

	if _, err := ResolvePathFromSlug("feature", ""); err == nil {
		t.Fatalf("expected ResolvePathFromSlug to fail when cwd is empty")
	}
}

func TestResolvePathFromSlugInDirRequiresDir(t *testing.T) {
	t.Parallel()

	if _, err := ResolvePathFromSlugInDir("feature", ""); err == nil {
		t.Fatalf("expected ResolvePathFromSlugInDir to fail when dir is empty")
	}
}
