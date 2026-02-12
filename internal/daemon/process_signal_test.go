//go:build darwin || linux

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// startHelperProcess launches the test binary in signal-helper mode via
// pty.Start (matching production). The mode parameter maps to
// DEV_TEST_SIGNAL_HELPER values defined in testmain_test.go:
//
//   - "graceful":   parent catches SIGINT, kills child, exits 0
//   - "ignore":     parent ignores SIGINT, child has default handling
//   - "ignore-all": both parent and child ignore SIGINT
func startHelperProcess(t *testing.T, mode string) (*processInfo, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		"DEV_TEST_SIGNAL_HELPER="+mode,
		"CHILD_PID_FILE="+pidFile,
	)
	configureManagedProcess(cmd)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })

	info := &processInfo{
		cmd:    cmd,
		exited: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(info.exited)
	}()

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	return info, childPID
}

// exitSignal returns the signal that terminated the process, or 0 if it
// exited normally.
func exitSignal(cmd *exec.Cmd) syscall.Signal {
	if cmd.ProcessState == nil {
		return 0
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0
	}
	return ws.Signal()
}

// TestStopGraceful verifies that a well-behaved process receives SIGINT and
// can shut down its children on its own terms without escalation.
func TestStopGraceful(t *testing.T) {
	info, childPID := startHelperProcess(t, "graceful")
	stopManagedProcess(info)

	if processExists(childPID) {
		t.Fatal("expected child to be dead after graceful stop")
	}
	if sig := exitSignal(info.cmd); sig != 0 {
		t.Fatalf("expected clean exit, got signal %v", sig)
	}
}

// TestStopEscalatesToGroupInterrupt verifies that when the main process
// ignores SIGINT, the group-wide SIGINT escalation kills children that
// have default signal handling (like postgres workers that set their own
// signal handlers via sigaction).
func TestStopEscalatesToGroupInterrupt(t *testing.T) {
	info, childPID := startHelperProcess(t, "ignore")
	stopManagedProcess(info)

	if processExists(childPID) {
		t.Fatal("expected child to be dead after group interrupt")
	}
	if sig := exitSignal(info.cmd); sig == syscall.SIGKILL {
		t.Fatal("expected process to exit via group INT, not SIGKILL")
	}
}

// TestStopEscalatesToKill verifies that when the entire process group ignores
// SIGINT, the final SIGKILL escalation cleans up everything.
func TestStopEscalatesToKill(t *testing.T) {
	info, childPID := startHelperProcess(t, "ignore-all")
	stopManagedProcess(info)

	if processExists(childPID) {
		t.Fatal("expected child to be dead after SIGKILL")
	}
	if sig := exitSignal(info.cmd); sig != syscall.SIGKILL {
		t.Fatalf("expected SIGKILL, got signal %v", sig)
	}
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
					return pid
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for child pid file")
	return 0
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
