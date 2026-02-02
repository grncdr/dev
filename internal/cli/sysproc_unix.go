//go:build darwin || linux

package cli

import "syscall"

type syscallSysProcAttr = syscall.SysProcAttr

func getSysProcAttr() syscallSysProcAttr {
	return syscallSysProcAttr{Setpgid: true}
}
