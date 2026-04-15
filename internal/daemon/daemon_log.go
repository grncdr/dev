package daemon

import (
	"fmt"
	"os"
	"strings"

	"dev/internal/worktree"
)

const daemonLogPathEnv = "DEV_DAEMON_LOG"

var daemonLogPathLookup = defaultDaemonLogPath

func defaultDaemonLogPath() string {
	return strings.TrimSpace(os.Getenv(daemonLogPathEnv))
}

func writeDaemonLogLine(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	line = line + "\n"
	path := daemonLogPathLookup()
	if path == "" {
		_, _ = os.Stdout.WriteString(line)
		return
	}
	if err := appendLineToCappedPath(path, line, managedLogMaxBytes); err != nil {
		_, _ = os.Stdout.WriteString(line)
	}
}

func logProcessEvent(action, project, slug, process string, pid int, network, address string) {
	msg := fmt.Sprintf("process %s %s pid=%d", action, worktree.FormatProcessIdentifier(project, slug, process), pid)
	if network != "" && address != "" {
		msg = fmt.Sprintf("%s target=%s:%s", msg, network, address)
	}
	writeDaemonLogLine(msg)
}

func logProcessExit(project, slug, process string, pid int, exitCode int, err error) {
	msg := fmt.Sprintf("process exit %s pid=%d code=%d", worktree.FormatProcessIdentifier(project, slug, process), pid, exitCode)
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
