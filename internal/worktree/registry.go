package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dev/internal/config"
)

const worktreeRegistryFileName = "worktree-registry.json"

type Registration struct {
	Project  string `json:"project"`
	Slug     string `json:"slug"`
	Path     string `json:"path"`
	MainPath string `json:"main_path,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

type registryState struct {
	Worktrees []Registration `json:"worktrees"`
}

func Register(daemonCfg *config.DaemonConfig, entry Registration) error {
	entry.Project = strings.TrimSpace(entry.Project)
	entry.Slug = strings.TrimSpace(entry.Slug)
	entry.Path = strings.TrimSpace(entry.Path)
	if entry.Project == "" || entry.Slug == "" || entry.Path == "" {
		return errors.New("project, slug, and path are required")
	}

	path, err := registryPath(daemonCfg)
	if err != nil {
		return err
	}
	state, err := loadRegistryState(path)
	if err != nil {
		return err
	}

	updated := false
	for i := range state.Worktrees {
		wt := state.Worktrees[i]
		if wt.Project == entry.Project && wt.Slug == entry.Slug {
			state.Worktrees[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		state.Worktrees = append(state.Worktrees, entry)
	}
	sort.Slice(state.Worktrees, func(i, j int) bool {
		left := state.Worktrees[i].Project + ":" + state.Worktrees[i].Slug
		right := state.Worktrees[j].Project + ":" + state.Worktrees[j].Slug
		return left < right
	})
	return saveRegistryState(path, state)
}

func Unregister(daemonCfg *config.DaemonConfig, project, slug string) error {
	path, err := registryPath(daemonCfg)
	if err != nil {
		return err
	}
	state, err := loadRegistryState(path)
	if err != nil {
		return err
	}
	filtered := state.Worktrees[:0]
	for _, wt := range state.Worktrees {
		if wt.Project == project && wt.Slug == slug {
			continue
		}
		filtered = append(filtered, wt)
	}
	state.Worktrees = filtered
	return saveRegistryState(path, state)
}

func FindRegisteredWorktree(daemonCfg *config.DaemonConfig, project, slug string) (Registration, bool, error) {
	entries, err := ListRegisteredWorktrees(daemonCfg, project)
	if err != nil {
		return Registration{}, false, err
	}
	for _, entry := range entries {
		if entry.Project == project && entry.Slug == slug {
			return entry, true, nil
		}
	}
	return Registration{}, false, nil
}

func FindRegistrationByPath(daemonCfg *config.DaemonConfig, project, path string) (Registration, bool, error) {
	entries, err := ListRegisteredWorktrees(daemonCfg, project)
	if err != nil {
		return Registration{}, false, err
	}
	for _, entry := range entries {
		if samePath(entry.Path, path) {
			return entry, true, nil
		}
	}
	return Registration{}, false, nil
}

func ListRegisteredWorktrees(daemonCfg *config.DaemonConfig, project string) ([]Registration, error) {
	path, err := registryPath(daemonCfg)
	if err != nil {
		return nil, err
	}
	state, err := loadRegistryState(path)
	if err != nil {
		return nil, err
	}
	if project == "" {
		return append([]Registration(nil), state.Worktrees...), nil
	}
	filtered := make([]Registration, 0, len(state.Worktrees))
	for _, wt := range state.Worktrees {
		if wt.Project == project {
			filtered = append(filtered, wt)
		}
	}
	return filtered, nil
}

func registryPath(daemonCfg *config.DaemonConfig) (string, error) {
	stateDir, err := config.ResolveStateDir(daemonCfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, worktreeRegistryFileName), nil
}

func loadRegistryState(path string) (*registryState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &registryState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read worktree registry: %w", err)
	}
	var state registryState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode worktree registry: %w", err)
	}
	if state.Worktrees == nil {
		state.Worktrees = []Registration{}
	}
	return &state, nil
}

func saveRegistryState(path string, state *registryState) error {
	if state == nil {
		state = &registryState{}
	}
	if len(state.Worktrees) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove worktree registry: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create worktree registry dir: %w", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode worktree registry: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write worktree registry: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace worktree registry: %w", err)
	}
	return nil
}

func samePath(a, b string) bool {
	aa, err := canonicalPath(a)
	if err != nil {
		return a == b
	}
	bb, err := canonicalPath(b)
	if err != nil {
		return a == b
	}
	return aa == bb
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil
	}
	return resolved, nil
}
