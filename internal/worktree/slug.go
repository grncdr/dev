package worktree

import (
	"errors"
	"path/filepath"
	"strings"
)

// Entry is a git worktree as reported by "git worktree list".
type Entry struct {
	// Path is the filesystem path of the worktree checkout.
	Path string
	// Branch is the checked-out branch name.
	Branch string
}

func ResolveSlug(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("cwd is required")
	}
	entries, err := listWorktreesInDir(cwd)
	if err != nil {
		return "", err
	}

	entry, err := matchWorktree(entries, cwd)
	if err != nil {
		return "", err
	}

	slug := filepath.Base(entry.Path)
	if slug == "." || slug == string(filepath.Separator) || slug == "" {
		return "", errors.New("invalid worktree slug")
	}

	return slug, nil
}

func matchWorktree(entries []Entry, cwd string) (Entry, error) {
	if strings.TrimSpace(cwd) == "" {
		return Entry{}, errors.New("cwd is required")
	}

	cwd, err := canonicalPath(cwd)
	if err != nil {
		return Entry{}, err
	}

	var best Entry
	bestLen := -1
	for _, entry := range entries {
		path := entry.Path
		if path == "" {
			continue
		}
		path, err = canonicalPath(path)
		if err != nil {
			continue
		}
		if pathContains(cwd, path) {
			if len(path) > bestLen {
				best = entry
				bestLen = len(path)
			}
		}
	}

	if bestLen == -1 {
		return Entry{}, errors.New("current directory is not a git worktree")
	}

	return best, nil
}

func pathContains(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+string(filepath.Separator))
}
