package daemon

import (
	"dev/internal/config"
	"os"
)

const SocketPath = "~/.config/dev/devd.sock"

func ResolveSocketPath() (string, error) {
	return config.ExpandUserPath(SocketPath)
}

func removeStaleSocket(socketPath string) error {
	if _, err := os.Stat(socketPath); err != nil {
		return nil
	}
	return os.Remove(socketPath)
}
