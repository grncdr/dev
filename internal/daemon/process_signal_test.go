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
)

func TestStopManagedProcessSignalsProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := "trap '' INT; sleep 60 & child=$!; echo $child > \"$CHILD_PID_FILE\"; wait $child"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "CHILD_PID_FILE="+pidFile)
	configureManagedProcess(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process: %v", err)
	}

	info := &processInfo{
		cmd:    cmd,
		exited: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(info.exited)
	}()

	childPID := waitForChildPID(t, pidFile)
	t.Cleanup(func() {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
	})

	stopManagedProcess(info)

	waitUntil(t, 2*time.Second, func() bool {
		return !processExists(childPID)
	}, "expected child process to exit after stop")
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitUntil(t, 2*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return false
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return false
		}
		pid = parsed
		return true
	}, "timed out waiting for child pid file")
	return pid
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}

func waitUntil(t *testing.T, timeout time.Duration, check func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !check() {
		t.Fatal(msg)
	}
}
