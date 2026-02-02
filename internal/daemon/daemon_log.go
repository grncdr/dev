package daemon

import (
	"fmt"
	"os"
	"strings"
)

const daemonLogPathEnv = "DEV_MODE_DAEMON_LOG"

func writeDaemonLogLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	line = line + "\n"
	path := strings.TrimSpace(os.Getenv(daemonLogPathEnv))
	if path == "" {
		_, _ = os.Stdout.WriteString(line)
		return
	}
	if err := appendLineToCappedPath(path, line, managedLogMaxBytes); err != nil {
		_, _ = os.Stdout.WriteString(line)
	}
}

func logProcessEvent(action, slug, process string, pid int, network, address string) {
	msg := fmt.Sprintf("process %s slug=%s name=%s pid=%d", action, slug, process, pid)
	if network != "" && address != "" {
		msg = fmt.Sprintf("%s target=%s:%s", msg, network, address)
	}
	writeDaemonLogLine(msg)
}

func logProcessExit(slug, process string, pid int, exitCode int, err error) {
	msg := fmt.Sprintf("process exit slug=%s name=%s pid=%d code=%d", slug, process, pid, exitCode)
	if err != nil {
		msg = fmt.Sprintf("%s err=%s", msg, err.Error())
	}
	writeDaemonLogLine(msg)
}

func logError(code int, errCode string, err error) {
	if err == nil {
		return
	}
	writeDaemonLogLine(fmt.Sprintf("daemon error status=%d code=%s err=%s", code, errCode, err.Error()))
}
