package worktree

import (
	"fmt"
	"path/filepath"
	"strings"

	"dev/internal/config"
)

func ResolveConfiguredMainSlug(mainPath string) (string, bool, error) {
	cfgPath := filepath.Join(mainPath, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return "", false, err
	}
	raw := strings.TrimSpace(cfg.Project.MainSlug)
	if raw == "" {
		return "", false, nil
	}
	slug, err := NormalizeIdentifierSegment(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid project.main_slug: %w", err)
	}
	return slug, true, nil
}

// ResolveMainSlug returns the configured main slug, falling back to "main".
func ResolveMainSlug(mainPath string) (string, error) {
	if configured, ok, err := ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if ok {
		return configured, nil
	}
	return "main", nil
}
