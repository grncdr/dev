package cli

import (
	"strings"

	"dev/internal/config"
	"dev/internal/worktree"
)

func proxyApexZone(daemonCfg *config.DaemonConfig) string {
	if daemonCfg != nil && strings.TrimSpace(daemonCfg.LocalProxy.ApexZone) != "" {
		return daemonCfg.LocalProxy.ApexZone
	}
	return ".localhost"
}

func effectiveDisplaySlug(slug, projectPath string, cfg *config.ProjectConfig) string {
	_ = projectPath
	return worktree.ProxyDNSLabelForSlug(cfg, slug)
}
