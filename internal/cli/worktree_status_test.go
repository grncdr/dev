package cli

import (
	"reflect"
	"testing"

	"dev/internal/config"
	"dev/internal/daemon"
)

func TestRouteMappingLinesSorted(t *testing.T) {
	byProcess := map[string][]string{
		"web": {
			"https://b.main.localhost/",
			"https://a.main.localhost/admin",
		},
		"jobs": {
			"https://a.main.localhost/admin",
		},
	}

	got := routeMappingLines(byProcess)
	want := []string{
		"https://a.main.localhost",
		"  /admin -> jobs",
		"  /admin -> web",
		"https://b.main.localhost -> web",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routeMappingLines mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestRouteMappingLinesIncludesUngroupedRoutes(t *testing.T) {
	byProcess := map[string][]string{
		"db": {"tcp://127.0.0.1:15432"},
	}
	got := routeMappingLines(byProcess)
	want := []string{"tcp://127.0.0.1:15432 -> db"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routeMappingLines mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestFormatProcessLine(t *testing.T) {
	cases := []struct {
		name         string
		proc         daemon.ProcessStatus
		fromWorktree string
		want         string
	}{
		{
			name: "running_with_pid",
			proc: daemon.ProcessStatus{Name: "rails", Status: "running", PID: 1234},
			fromWorktree: "",
			want: "rails (running: PID 1234)",
		},
		{
			name: "non_running_with_pid",
			proc: daemon.ProcessStatus{Name: "worker", Status: "exited", PID: 789},
			fromWorktree: "",
			want: "worker (exited: PID 789)",
		},
		{
			name: "without_pid",
			proc: daemon.ProcessStatus{Name: "webpack", Status: "stopped"},
			fromWorktree: "",
			want: "webpack (stopped)",
		},
		{
			name: "from_worktree",
			proc: daemon.ProcessStatus{Name: "postgres", Status: "stopped"},
			fromWorktree: "main",
			want: "postgres (stopped) (from worktree: main)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatProcessLine(tc.proc, tc.fromWorktree); got != tc.want {
				t.Fatalf("formatProcessLine mismatch:\nwant: %q\ngot:  %q", tc.want, got)
			}
		})
	}
}

func TestResolveProcessStatusSingletonUsesMainWorktreeStatus(t *testing.T) {
	cfg := &config.ProjectConfig{
		Processes: map[string]map[string]any{
			"db": {"singleton": true},
		},
	}
	current := map[string]daemon.ProcessStatus{}
	main := map[string]daemon.ProcessStatus{
		"db": {Name: "db", Status: "running", PID: 3456},
	}
	got, from := resolveProcessStatus("db", current, main, cfg, false, "primary")
	if got.Status != "running" || got.PID != 3456 {
		t.Fatalf("expected running singleton from main, got %+v", got)
	}
	if from != "primary" {
		t.Fatalf("expected from worktree primary, got %q", from)
	}
}

func TestResolveProcessStatusNonSingletonUsesLocalStatus(t *testing.T) {
	cfg := &config.ProjectConfig{
		Processes: map[string]map[string]any{
			"web": {"singleton": false},
		},
	}
	current := map[string]daemon.ProcessStatus{
		"web": {Name: "web", Status: "running", PID: 7890},
	}
	main := map[string]daemon.ProcessStatus{
		"web": {Name: "web", Status: "stopped"},
	}
	got, from := resolveProcessStatus("web", current, main, cfg, false, "main")
	if got.Status != "running" || got.PID != 7890 {
		t.Fatalf("expected local running status, got %+v", got)
	}
	if from != "" {
		t.Fatalf("expected no from-worktree marker, got %q", from)
	}
}
