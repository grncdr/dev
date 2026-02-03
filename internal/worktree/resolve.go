package worktree

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// validSlugPattern matches slugs containing alphanumeric characters, hyphens, underscores, and slashes.
// Must start with an alphanumeric character.
var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_-]*$`)

// ValidateSlug checks that a slug contains only safe characters.
func ValidateSlug(slug string) error {
	if slug == "" {
		return errors.New("slug is required")
	}
	if !validSlugPattern.MatchString(slug) {
		return errors.New("invalid slug: must start with alphanumeric and contain only alphanumeric, hyphens, underscores, or slashes")
	}
	return nil
}

func ResolvePathFromSlug(slug, cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("cwd is required")
	}
	return ResolvePathFromSlugInDir(slug, cwd)
}

func ResolvePathFromSlugInDir(slug, dir string) (string, error) {
	if slug == "" {
		return "", errors.New("slug is required")
	}
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("dir is required")
	}

	// Allow qualified slugs like project/slug; use the last segment for matching.
	if strings.Contains(slug, "/") {
		parts := strings.Split(slug, "/")
		slug = parts[len(parts)-1]
	}

	if err := ValidateSlug(slug); err != nil {
		return "", err
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
