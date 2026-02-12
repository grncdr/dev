package agent

// ProxyTarget is a resolved upstream process that a proxy request will be
// forwarded to. Returned by Manager.ensureProxyTargetForRuntime.
type ProxyTarget struct {
	// Network is the dialing network ("tcp" or "unix").
	Network string
	// Address is the dialing address (e.g. "127.0.0.1:3000" or a socket path).
	Address string
	// Slug is the worktree slug that owns this process.
	Slug string
	// Path is the filesystem path of the worktree.
	Path string
	// Process is the process name from the project config.
	Process string
}
