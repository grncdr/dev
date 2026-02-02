//go:build windows

package cli

type syscallSysProcAttr struct{}

func getSysProcAttr() syscallSysProcAttr {
	return syscallSysProcAttr{}
}
