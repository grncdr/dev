package worktree

import "testing"

func TestParseProjectSlug(t *testing.T) {
	target, err := ParseProjectSlug("Foo_Bar/Feature-1")
	if err != nil {
		t.Fatalf("ParseProjectSlug: %v", err)
	}
	if target.Project != "foo_bar" {
		t.Fatalf("expected normalized project, got %q", target.Project)
	}
	if target.Slug != "feature-1" {
		t.Fatalf("expected normalized slug, got %q", target.Slug)
	}
}

func TestParseProjectSlugInvalid(t *testing.T) {
	cases := []string{
		"",
		"missing",
		"project/",
		"/slug",
		"a/b/c",
		"dot.slug/ok",
		"ok/white space",
	}
	for _, tc := range cases {
		if _, err := ParseProjectSlug(tc); err == nil {
			t.Fatalf("expected error for %q", tc)
		}
	}
}
