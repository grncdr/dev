package daemon

import "dev-mode/internal/config"

const SocketPath = "~/.config/dev-mode/devd.sock"

func ResolveSocketPath() (string, error) {
	return config.ExpandUserPath(SocketPath)
}
