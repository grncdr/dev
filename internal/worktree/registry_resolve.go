package worktree

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"dev/internal/config"
)

func ResolveDefaultSlug(cwd string, daemonCfg *config.DaemonConfig) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("cwd is required")
	}
	entries, err := ListWorktreesInDir(cwd)
	if err != nil {
		return "", fmt.Errorf("slug is required: %w", err)
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return "", errors.New("main worktree path missing")
	}
	current, err := matchWorktree(entries, cwd)
	if err != nil {
		return "", err
	}
	mainPath := entries[0].Path
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return "", err
	}
	if err := ensureProjectState(daemonCfg, project, mainPath); err != nil {
		return "", err
	}
	if samePath(current.Path, mainPath) {
		if configuredMainSlug, ok, err := ResolveConfiguredMainSlug(mainPath); err != nil {
			return "", err
		} else if ok {
			return configuredMainSlug, nil
		}
		return "main", nil
	}
	registered, ok, err := FindRegistrationByPath(daemonCfg, project, current.Path)
	if err != nil {
		return "", err
	}
	if ok {
		return registered.Slug, nil
	}
	return "", errors.New("slug is required: current worktree is not registered (run `dev worktree register` in this worktree or pass an explicit slug)")
}

func ResolvePathFromSlugWithRegistry(slug, cwd string, daemonCfg *config.DaemonConfig) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("cwd is required")
	}
	entries, err := ListWorktreesInDir(cwd)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return "", errors.New("main worktree path missing")
	}
	mainPath := entries[0].Path
	if slug == "main" {
		if project, err := resolveProjectIdentifierFromMainPath(mainPath); err == nil {
			_ = ensureProjectState(daemonCfg, project, mainPath)
		}
		return mainPath, nil
	}
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return "", err
	}
	if err := ensureProjectState(daemonCfg, project, mainPath); err != nil {
		return "", err
	}
	if configuredMainSlug, ok, err := ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if ok && slug == configuredMainSlug {
		return mainPath, nil
	}
	if registered, ok, err := FindRegisteredWorktree(daemonCfg, project, slug); err != nil {
		return "", err
	} else if ok {
		return registered.Path, nil
	}
	return "", fmt.Errorf("worktree slug %q is not registered in dev state", slug)
}

func resolveProjectIdentifierFromMainPath(mainPath string) (string, error) {
	cfgPath := filepath.Join(mainPath, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return "", err
	}
	return NormalizeIdentifierSegment(cfg.Project.Name)
}
