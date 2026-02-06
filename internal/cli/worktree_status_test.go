package cli

import (
	"reflect"
	"testing"

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
