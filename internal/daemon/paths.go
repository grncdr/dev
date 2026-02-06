package daemon

import "dev/internal/config"

const SocketPath = "~/.config/dev/devd.sock"

func ResolveSocketPath() (string, error) {
	return config.ExpandUserPath(SocketPath)
}
