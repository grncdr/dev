package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Entry struct {
	Path   string
	Branch string
}

func ResolveSlug(cwd string) (string, error) {
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
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Entry{}, err
		}
	}

	cwd, err := filepath.Abs(cwd)
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
		path, err = filepath.Abs(path)
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
