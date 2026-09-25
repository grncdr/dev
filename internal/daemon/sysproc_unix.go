//go:build darwin || linux

package daemon

import "syscall"

type syscallSysProcAttr = syscall.SysProcAttr

func getSysProcAttr() syscallSysProcAttr {
	return syscallSysProcAttr{Setpgid: true}
}
