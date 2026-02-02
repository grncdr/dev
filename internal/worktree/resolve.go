package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ResolvePathFromSlug(slug string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return ResolvePathFromSlugInDir(slug, cwd)
}

func ResolvePathFromSlugInDir(slug, dir string) (string, error) {
	if slug == "" {
		return "", errors.New("slug is required")
	}

	// Allow qualified slugs like project/slug; use the last segment for matching.
	if strings.Contains(slug, "/") {
		parts := strings.Split(slug, "/")
		slug = parts[len(parts)-1]
	}

	entries, err := listWorktreesInDir(dir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if filepath.Base(entry.Path) == slug {
			return entry.Path, nil
		}
	}

	return "", errors.New("worktree slug not found")
}

func ResolveMainSlugInDir(dir string) (string, error) {
	return mainSlugFromDir(dir)
}
