package config

// GlobalFlags holds the global CLI flags and resolved state shared across all commands.
type GlobalFlags struct {
	// ConfigPath is the --config flag value (defaults to ".dev.toml").
	ConfigPath string
	// DaemonConfigPath is the --daemon-config flag value.
	DaemonConfigPath string
	// Debug enables verbose logging when true.
	Debug bool
	// WorkingDir is the process working directory at startup.
	WorkingDir string
	// ResolvedPaths holds the absolute paths computed from the flag values.
	ResolvedPaths ResolvedPaths
}

// ResolvedPaths holds absolute filesystem paths for the project and daemon configs,
// resolved from the CLI flag values during PersistentPreRunE.
type ResolvedPaths struct {
	// ProjectConfig is the absolute path to .dev.toml.
	ProjectConfig string
	// DaemonConfig is the absolute path to daemon.toml.
	DaemonConfig string
}
