package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"dev-mode/internal/config"
)

const resumeFileName = "resume.json"

type resumeState struct {
	Worktrees []resumeWorktree `json:"worktrees"`
}

type resumeWorktree struct {
	Slug string `json:"slug"`
	Path string `json:"path,omitempty"`
}

func (m *Manager) runningWorktrees() []resumeWorktree {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]resumeWorktree, 0, len(m.processes))
	for slug, procs := range m.processes {
		running := false
		for _, info := range procs {
			if info != nil && info.cmd != nil && info.cmd.Process != nil {
				running = true
				break
			}
		}
		if !running {
			continue
		}
		entry := resumeWorktree{Slug: slug}
		if path, ok := m.paths[slug]; ok {
			entry.Path = path
		}
		out = append(out, entry)
	}
	return out
}

func resumeStatePath() (string, error) {
	root, err := daemonStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, resumeFileName), nil
}

func daemonStateDir() (string, error) {
	return config.ExpandUserPath("~/.local/state/dev-mode")
}

func saveResumeState(worktrees []resumeWorktree) error {
	path, err := resumeStatePath()
	if err != nil {
		return err
	}
	if len(worktrees) == 0 {
		_ = os.Remove(path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(resumeState{Worktrees: worktrees}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadResumeState() ([]resumeWorktree, error) {
	path, err := resumeStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state resumeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state.Worktrees, nil
}

func clearResumeState() error {
	path, err := resumeStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
