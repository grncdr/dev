package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonStartFailureIncludesLogTail(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	content := strings.Join([]string{
		"old line",
		"daemon error status=500 code=proxy_listen_failed err=proxy http listen: listen tcp 127.0.0.1:43123: bind: address already in use",
		"final line",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	err := daemonStartFailure(errors.New("exit status 1"), errors.New("dial unix /tmp/dev.sock: connect: connection refused"), logPath)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "daemon failed to start: exit status 1") {
		t.Fatalf("expected startup cause in error, got %q", msg)
	}
	if !strings.Contains(msg, "last health check") {
		t.Fatalf("expected health check detail in error, got %q", msg)
	}
	if !strings.Contains(msg, "proxy http listen") {
		t.Fatalf("expected daemon log tail in error, got %q", msg)
	}
	if !strings.Contains(msg, logPath) {
		t.Fatalf("expected log path in error, got %q", msg)
	}
}

func TestLastLogLinesReturnsTail(t *testing.T) {
	got := lastLogLines([]byte("one\ntwo\nthree\nfour\n"), 2)
	if got != "three\nfour" {
		t.Fatalf("lastLogLines() = %q, want %q", got, "three\nfour")
	}
}
