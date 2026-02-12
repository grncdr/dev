//go:build windows

package daemon

import (
	"os"
	"os/exec"
)

func configureManagedProcess(cmd *exec.Cmd) {}

func interruptManagedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(os.Interrupt)
}

func interruptProcessGroup(cmd *exec.Cmd) error {
	return interruptManagedProcess(cmd)
}

func killManagedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
