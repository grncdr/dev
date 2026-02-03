package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func listWorktrees() ([]Entry, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return listWorktreesInDir(cwd)
}

func ListWorktreesInDir(dir string) ([]Entry, error) {
	return listWorktreesInDir(dir)
}

func listWorktreesInDir(dir string) ([]Entry, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list failed: %w", err)
	}
	return parseWorktreeList(string(output))
}

func parseWorktreeList(output string) ([]Entry, error) {
	lines := strings.Split(output, "\n")
	entries := []Entry{}
	var current Entry
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" {
				entries = append(entries, current)
			}
			current = Entry{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
			continue
		}
		if strings.HasPrefix(line, "branch ") {
			current.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		}
	}
	if current.Path != "" {
		entries = append(entries, current)
	}
	if len(entries) == 0 {
		return nil, errors.New("no worktrees found")
	}
	return entries, nil
}

func BranchName(ref string) string {
	value := strings.TrimSpace(ref)
	return strings.TrimPrefix(value, "refs/heads/")
}

func ResolveMainPathInDir(dir string) (string, error) {
	entries, err := listWorktreesInDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 || entries[0].Path == "" {
		return "", errors.New("main worktree path missing")
	}
	return entries[0].Path, nil
}

func mainSlugFromDir(dir string) (string, error) {
	entries, err := listWorktreesInDir(dir)
	if err != nil {
		return "", err
	}
	return mainSlugFromEntries(entries)
}

func mainSlugFromEntries(entries []Entry) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("no worktrees found")
	}
	mainPath := entries[0].Path
	if mainPath == "" {
		return "", errors.New("main worktree path missing")
	}
	return filepath.Base(mainPath), nil
}
