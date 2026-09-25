//go:build windows

package daemon

type syscallSysProcAttr struct{}

func getSysProcAttr() syscallSysProcAttr {
	return syscallSysProcAttr{}
}
