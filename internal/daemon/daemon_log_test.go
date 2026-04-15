package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogProcessEventUsesQualifiedIdentifier(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	daemonLogPathLookup = func() string { return logPath }
	t.Cleanup(func() {
		daemonLogPathLookup = defaultDaemonLogPath
	})

	logProcessEvent("start", "demo", "feature/api", "web", 1234, "tcp", "127.0.0.1:3000")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "process start demo:feature/api:web pid=1234 target=tcp:127.0.0.1:3000"
	if got != want {
		t.Fatalf("unexpected log line %q, want %q", got, want)
	}
}

func TestLogProcessExitUsesQualifiedIdentifier(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	daemonLogPathLookup = func() string { return logPath }
	t.Cleanup(func() {
		daemonLogPathLookup = defaultDaemonLogPath
	})

	logProcessExit("demo", "feature/api", "web", 1234, 1, errors.New("boom"))

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "process exit demo:feature/api:web pid=1234 code=1 err=boom"
	if got != want {
		t.Fatalf("unexpected log line %q, want %q", got, want)
	}
}
