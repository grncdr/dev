package cli

import "testing"

func TestParseAttachTarget(t *testing.T) {
	cases := []struct {
		input   string
		slug    string
		process string
		project string
		ok      bool
	}{
		{"rails", "", "rails", "", true},
		{"feature:rails", "feature", "rails", "", true},
		{"proj/feature:rails", "feature", "rails", "proj", true},
		{"proj/feature:*", "feature", "*", "proj", true},
		{":rails", "", "", "", false},
		{"feature:", "", "", "", false},
		{"proj/:rails", "", "", "", false},
	}
	for _, tc := range cases {
		slug, process, project, err := parseAttachTarget(tc.input)
		if tc.ok && err != nil {
			t.Fatalf("expected ok for %q, got %v", tc.input, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("expected error for %q", tc.input)
		}
		if tc.ok {
			if slug != tc.slug || process != tc.process || project != tc.project {
				t.Fatalf("unexpected parse for %q: %q %q %q", tc.input, slug, process, project)
			}
		}
	}
}
