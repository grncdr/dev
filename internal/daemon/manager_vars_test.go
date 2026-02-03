package daemon

import "testing"

func TestBuildVarsIncludesDevModeProjectAndSlug(t *testing.T) {
	vars := buildVars("Foo Corp", "feature-123", "/repo/feature-123", "/repo/main", "/state/worktree", ".localhost")

	if vars["DEV_MODE_PROJECT"] != "Foo Corp" {
		t.Fatalf("expected DEV_MODE_PROJECT, got %q", vars["DEV_MODE_PROJECT"])
	}
	if vars["DEV_MODE_WORKTREE_SLUG"] != "feature-123" {
		t.Fatalf("expected DEV_MODE_WORKTREE_SLUG, got %q", vars["DEV_MODE_WORKTREE_SLUG"])
	}
}

func TestBuildVarsLocalDNSName(t *testing.T) {
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
		vars := buildVars("proj", tc.slug, "/repo", "/main", "/state", tc.apexZone)
		if vars["DEV_MODE_LOCAL_DNS_NAME"] != tc.expected {
			t.Errorf("slug=%q apexZone=%q: expected DEV_MODE_LOCAL_DNS_NAME=%q, got %q",
				tc.slug, tc.apexZone, tc.expected, vars["DEV_MODE_LOCAL_DNS_NAME"])
		}
	}
}
