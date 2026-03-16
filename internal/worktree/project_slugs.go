package worktree

import (
	"sort"
	"strings"

	"dev/internal/config"
)

// ListProjectSlugsFromRegistry returns known slugs for a project from daemon
// registry state, including the project's configured main slug when available.
func ListProjectSlugsFromRegistry(daemonCfg *config.DaemonConfig, project string) ([]string, error) {
	projectID, err := normalizeProject(project)
	if err != nil {
		return nil, err
	}
	entries, err := ListRegisteredWorktrees(daemonCfg, projectID)
	if err != nil {
		return nil, err
	}

	slugSet := map[string]struct{}{}
	for _, entry := range entries {
		slug := strings.TrimSpace(entry.Slug)
		if slug == "" {
			continue
		}
		slugSet[slug] = struct{}{}
	}

	if mainPath, ok, err := findProjectMainPathFromRegistry(daemonCfg, projectID); err != nil {
		return nil, err
	} else if ok {
		mainSlug := "main"
		if configuredMainSlug, hasConfigured, err := ResolveConfiguredMainSlug(mainPath); err != nil {
			return nil, err
		} else if hasConfigured {
			mainSlug = configuredMainSlug
		}
		slugSet[mainSlug] = struct{}{}
	}

	slugs := make([]string, 0, len(slugSet))
	for slug := range slugSet {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs, nil
}
