package worktree

import "testing"

func TestFormatProcessIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		project string
		slug    string
		process string
		want    string
	}{
		{
			name:    "fully qualified",
			project: "demo",
			slug:    "feature/api",
			process: "web",
			want:    "demo:feature/api:web",
		},
		{
			name:    "omits empty project",
			slug:    "feature/api",
			process: "web",
			want:    "feature/api:web",
		},
		{
			name:    "omits empty slug",
			process: "web",
			want:    "web",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatProcessIdentifier(tc.project, tc.slug, tc.process); got != tc.want {
				t.Fatalf("FormatProcessIdentifier(%q, %q, %q) = %q, want %q", tc.project, tc.slug, tc.process, got, tc.want)
			}
		})
	}
}
