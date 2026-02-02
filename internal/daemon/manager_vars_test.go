package daemon

import "testing"

func TestBuildVarsIncludesDevModeProjectAndSlug(t *testing.T) {
	vars := buildVars("Foo Corp", "feature-123", "/repo/feature-123", "/repo/main", "/state/worktree")

	if vars["DEV_MODE_PROJECT"] != "Foo Corp" {
		t.Fatalf("expected DEV_MODE_PROJECT, got %q", vars["DEV_MODE_PROJECT"])
	}
	if vars["DEV_MODE_WORKTREE_SLUG"] != "feature-123" {
		t.Fatalf("expected DEV_MODE_WORKTREE_SLUG, got %q", vars["DEV_MODE_WORKTREE_SLUG"])
	}
}
