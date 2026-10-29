package worktree

import (
	"strings"

	"dev/internal/config"
)

// RuntimeEnvironment returns the core worktree variables injected into managed
// processes and start/stop hooks.
func RuntimeEnvironment(cfg *config.ProjectConfig, project, slug, path, branch, apexZone string) map[string]string {
	dnsLabel := ProxyDNSLabelForSlug(cfg, slug)
	zone := strings.TrimPrefix(strings.TrimSpace(apexZone), ".")
	if zone == "" {
		zone = "localhost"
	}
	return map[string]string{
		"DEV_PROJECT":           project,
		"DEV_WORKTREE_SLUG":     slug,
		"DEV_WORKTREE_DNS_NAME": dnsLabel + "." + zone,
		"DEV_WORKTREE_PATH":     path,
		"DEV_WORKTREE_BRANCH":   branch,
	}
}
