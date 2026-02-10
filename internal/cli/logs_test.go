package cli

import (
	"strings"
	"testing"
)

func TestSelectLogsTargets(t *testing.T) {
	tests := []struct {
		name          string
		targets       []string
		wantProject   string
		wantSlug      string
		wantProcesses []string
		wantErr       string
	}{
		{
			name:          "empty targets",
			targets:       nil,
			wantProcesses: []string{},
		},
		{
			name:          "multiple process names",
			targets:       []string{"web", "worker"},
			wantProcesses: []string{"web", "worker"},
		},
		{
			name:          "dedupe duplicate process names",
			targets:       []string{"web", "web", "worker"},
			wantProcesses: []string{"web", "worker"},
		},
		{
			name:          "slug and process mix",
			targets:       []string{"demo:web", "worker"},
			wantSlug:      "demo",
			wantProcesses: []string{"web", "worker"},
		},
		{
			name:          "project slug process mix",
			targets:       []string{"proj:demo:web", "proj:demo:worker"},
			wantProject:   "proj",
			wantSlug:      "demo",
			wantProcesses: []string{"web", "worker"},
		},
		{
			name:    "conflicting slugs",
			targets: []string{"alpha:web", "beta:worker"},
			wantErr: "same slug",
		},
		{
			name:    "conflicting projects",
			targets: []string{"proj1:demo:web", "proj2:demo:worker"},
			wantErr: "same project",
		},
		{
			name:    "invalid target",
			targets: []string{"demo:"},
			wantErr: "process is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, slug, processes, err := selectLogsTargets(tc.targets)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectLogsTargets: %v", err)
			}
			if project != tc.wantProject {
				t.Fatalf("unexpected project: got %q want %q", project, tc.wantProject)
			}
			if slug != tc.wantSlug {
				t.Fatalf("unexpected slug: got %q want %q", slug, tc.wantSlug)
			}
			if len(processes) != len(tc.wantProcesses) {
				t.Fatalf("unexpected process count: got %d want %d (%v)", len(processes), len(tc.wantProcesses), processes)
			}
			for i := range processes {
				if processes[i] != tc.wantProcesses[i] {
					t.Fatalf("unexpected processes: got %v want %v", processes, tc.wantProcesses)
				}
			}
		})
	}
}
