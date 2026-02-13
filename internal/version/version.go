package version

// Version is populated at build time via -ldflags "-X dev/internal/version.Version=<version>".
// Default to "dev" for local builds.
var Version = "dev"

func String() string {
	return Version
}
