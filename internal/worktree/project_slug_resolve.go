package worktree

import (
	"fmt"
	"os"
	"strings"

	"dev/internal/config"
)

// ResolvePathFromProjectSlug resolves project-qualified worktree references
// without depending on the caller's current working directory.
func ResolvePathFromProjectSlug(project, slug string, daemonCfg *config.DaemonConfig) (string, error) {
	projectID, err := normalizeProject(project)
	if err != nil {
		return "", err
	}
	slugID, err := normalizeSlug(slug)
	if err != nil {
		return "", err
	}

	if registered, ok, err := FindRegisteredWorktree(daemonCfg, projectID, slugID); err != nil {
		return "", err
	} else if ok {
		return registered.Path, nil
	}

	mainPath, ok, err := findProjectMainPathFromRegistry(daemonCfg, projectID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("worktree %s:%s is not registered in dev state", projectID, slugID)
	}

	if slugID == "main" {
		return mainPath, nil
	}
	if configuredMainSlug, hasConfigured, err := ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if hasConfigured && slugID == configuredMainSlug {
		return mainPath, nil
	}

	path, err := ResolvePathFromSlugWithRegistry(slugID, mainPath, daemonCfg)
	if err != nil {
		return "", err
	}
	return path, nil
}

// ResolvePathWithProjectHint resolves a worktree path using an optional project hint.
// When project is provided, resolution does not depend on cwd.
func ResolvePathWithProjectHint(slug, project, cwd string, daemonCfg *config.DaemonConfig) (string, error) {
	if strings.TrimSpace(project) != "" {
		return ResolvePathFromProjectSlug(project, slug, daemonCfg)
	}
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return ResolvePathFromSlugWithRegistry(slug, cwd, daemonCfg)
}

func findProjectMainPathFromRegistry(daemonCfg *config.DaemonConfig, project string) (string, bool, error) {
	if mainPath, ok, err := findProjectMainPath(daemonCfg, project); err != nil {
		return "", false, err
	} else if ok {
		return mainPath, true, nil
	}
	return "", false, nil
}
