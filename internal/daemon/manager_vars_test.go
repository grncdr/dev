package daemon

import "testing"

func TestBuildRuntimeVarsCoreNames(t *testing.T) {
	vars := buildRuntimeVars("myproj", "feature/branch", "/repo/feature/branch", "feature/branch", ".localhost")

	if vars["DEV_PROJECT"] != "myproj" {
		t.Fatalf("expected DEV_PROJECT, got %q", vars["DEV_PROJECT"])
	}
	if vars["DEV_WORKTREE_SLUG"] != "feature/branch" {
		t.Fatalf("expected DEV_WORKTREE_SLUG, got %q", vars["DEV_WORKTREE_SLUG"])
	}
	if vars["DEV_WORKTREE_DNS_NAME"] != "branch.localhost" {
		t.Fatalf("expected DEV_WORKTREE_DNS_NAME, got %q", vars["DEV_WORKTREE_DNS_NAME"])
	}
	if vars["DEV_WORKTREE_PATH"] != "/repo/feature/branch" {
		t.Fatalf("expected DEV_WORKTREE_PATH, got %q", vars["DEV_WORKTREE_PATH"])
	}
	if vars["DEV_WORKTREE_BRANCH"] != "feature/branch" {
		t.Fatalf("expected DEV_WORKTREE_BRANCH, got %q", vars["DEV_WORKTREE_BRANCH"])
	}
}

func TestBuildRuntimeVarsDNSName(t *testing.T) {
	cases := []struct {
		slug     string
		apexZone string
		expected string
	}{
		{"feature-123", ".localhost", "feature-123.localhost"},
		{"feature/my-branch", ".localhost", "my-branch.localhost"},
		{"feature/sub/deep", ".example.test", "deep.example.test"},
		{"simple", "localhost", "simple.localhost"},
		{"main", "", "main.localhost"},
	}
	for _, tc := range cases {
		vars := buildRuntimeVars("proj", tc.slug, "/repo", "main", tc.apexZone)
		if vars["DEV_WORKTREE_DNS_NAME"] != tc.expected {
			t.Errorf("slug=%q apexZone=%q: expected DEV_WORKTREE_DNS_NAME=%q, got %q",
				tc.slug, tc.apexZone, tc.expected, vars["DEV_WORKTREE_DNS_NAME"])
		}
	}
}
