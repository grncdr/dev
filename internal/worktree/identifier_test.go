package worktree

import "testing"

func TestParseProjectSlug(t *testing.T) {
	target, err := ParseProjectSlug("Foo_Bar:Feature/Branch-1")
	if err != nil {
		t.Fatalf("ParseProjectSlug: %v", err)
	}
	if target.Project != "foo_bar" {
		t.Fatalf("expected normalized project, got %q", target.Project)
	}
	if target.Slug != "feature/branch-1" {
		t.Fatalf("expected normalized slug, got %q", target.Slug)
	}
}

func TestParseProjectSlugInvalid(t *testing.T) {
	cases := []string{
		"",
		"missing",
		"a/b",
		"project:",
		":slug",
		"dot.slug:ok",
		"ok:white space",
	}
	for _, tc := range cases {
		if _, err := ParseProjectSlug(tc); err == nil {
			t.Fatalf("expected error for %q", tc)
		}
	}
}

func TestSlugDNSLabel(t *testing.T) {
	cases := []struct {
		slug  string
		label string
	}{
		{"branch", "branch"},
		{"feature/branch", "branch"},
		{"feature/sub/deep", "deep"},
		{"", ""},
	}
	for _, tc := range cases {
		got := SlugDNSLabel(tc.slug)
		if got != tc.label {
			t.Fatalf("SlugDNSLabel(%q) = %q, want %q", tc.slug, got, tc.label)
		}
	}
}

func TestParseProcessIdentifier(t *testing.T) {
	cases := []struct {
		input   string
		slug    string
		process string
		project string
		ok      bool
	}{
		{"rails", "", "rails", "", true},
		{"feature:rails", "feature", "rails", "", true},
		{"feature/x:rails", "feature/x", "rails", "", true},
		{"proj:feature/x:rails", "feature/x", "rails", "proj", true},
		{"proj:x:*", "x", "*", "proj", true},
		{":rails", "", "", "", false},
		{"feature:", "", "", "", false},
		{"proj::rails", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, tc := range cases {
		id, err := ParseProcessIdentifier(tc.input)
		if tc.ok && err != nil {
			t.Fatalf("expected ok for %q, got %v", tc.input, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("expected error for %q", tc.input)
		}
		if tc.ok {
			if id.Slug != tc.slug || id.Process != tc.process || id.Project != tc.project {
				t.Fatalf("unexpected parse for %q: slug=%q process=%q project=%q", tc.input, id.Slug, id.Process, id.Project)
			}
		}
	}
}
