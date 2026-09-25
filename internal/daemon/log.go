package daemon

import (
	"dev/internal/config"
	"io"
	"os"
	"path/filepath"
)

const (
	maxLogBytes             = 20 * 1024 * 1024
	daemonStartLogTailBytes = 8 * 1024
	daemonStartLogTailLines = 20
)

func openLog(daemonConfig *config.DaemonConfig) (*os.File, string, error) {
	stateDir, err := config.ResolveStateDir(daemonConfig)
	if err != nil {
		return nil, "", err
	}
	logsDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, "", err
	}
	path := filepath.Join(logsDir, "daemon.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	if err := trimFileToMax(file, maxLogBytes); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, "", err
	}
	return file, path, nil
}

func trimFileToMax(file *os.File, maxBytes int64) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	start := info.Size() - maxBytes
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, maxBytes)
	if _, err := io.ReadFull(file, buf); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(buf); err != nil {
		return err
	}
	return nil
}
