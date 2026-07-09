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

const (
	legacyWorktreeRegistryFileName = "worktree-registry.json"
	projectRegistryFileName        = "worktree-projects.json"
)

// Registration is a worktree entry persisted in the worktree registry file.
// The registry tracks worktrees that have been explicitly added via "dev worktree add".
type Registration struct {
	// Identifier is the project:slug identity from the worktree's .dev.toml.
	Identifier
	// Path is the filesystem path of the worktree checkout.
	Path string `json:"path"`
	// MainPath is the path to the main worktree (set for non-main worktrees).
	MainPath string `json:"main_path,omitempty"`
	// Branch is the git branch name at registration time.
	Branch string `json:"branch,omitempty"`
}

type registryState struct {
	Projects []projectState `json:"projects"`
}

type projectState struct {
	Name      string               `json:"name"`
	MainPath  string               `json:"main_path,omitempty"`
	Worktrees []projectWorktreeRef `json:"worktrees,omitempty"`
}

type projectWorktreeRef struct {
	Slug   string `json:"slug"`
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
}

type legacyRegistryState struct {
	Worktrees  []Registration          `json:"worktrees"`
	Deprecated *legacyDeprecationState `json:"__deprecated__,omitempty"`
}

type legacyDeprecationState struct {
	Message    string `json:"message"`
	MigratedTo string `json:"migrated_to"`
}

func Register(daemonCfg *config.DaemonConfig, entry Registration) error {
	entry.Project = strings.TrimSpace(entry.Project)
	entry.Slug = strings.TrimSpace(entry.Slug)
	entry.Path = strings.TrimSpace(entry.Path)
	entry.MainPath = strings.TrimSpace(entry.MainPath)
	entry.Branch = strings.TrimSpace(entry.Branch)
	if entry.Project == "" || entry.Slug == "" || entry.Path == "" {
		return errors.New("project, slug, and path are required")
	}

	projectPath, legacyPath, err := registryPaths(daemonCfg)
	if err != nil {
		return err
	}
	state, err := loadRegistryState(projectPath, legacyPath)
	if err != nil {
		return err
	}
	upsertProjectWorktree(state, entry)
	return saveRegistryState(projectPath, legacyPath, state)
}

func Unregister(daemonCfg *config.DaemonConfig, project, slug string) error {
	projectPath, legacyPath, err := registryPaths(daemonCfg)
	if err != nil {
		return err
	}
	state, err := loadRegistryState(projectPath, legacyPath)
	if err != nil {
		return err
	}
	removeProjectWorktree(state, strings.TrimSpace(project), strings.TrimSpace(slug))
	return saveRegistryState(projectPath, legacyPath, state)
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
	projectPath, legacyPath, err := registryPaths(daemonCfg)
	if err != nil {
		return nil, err
	}
	state, err := loadRegistryState(projectPath, legacyPath)
	if err != nil {
		return nil, err
	}
	return flattenRegistryState(state, strings.TrimSpace(project)), nil
}

func registryPaths(daemonCfg *config.DaemonConfig) (string, string, error) {
	stateDir, err := config.ResolveStateDir(daemonCfg)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(stateDir, projectRegistryFileName), filepath.Join(stateDir, legacyWorktreeRegistryFileName), nil
}

func loadRegistryState(projectPath, legacyPath string) (*registryState, error) {
	data, err := os.ReadFile(projectPath)
	if errors.Is(err, os.ErrNotExist) {
		return migrateLegacyRegistry(projectPath, legacyPath)
	}
	if err != nil {
		return nil, fmt.Errorf("read worktree project registry: %w", err)
	}
	var state registryState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode worktree project registry: %w", err)
	}
	if state.Projects == nil {
		state.Projects = []projectState{}
	}
	normalizeRegistryState(&state)
	if err := markLegacyRegistryDeprecated(legacyPath); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveRegistryState(projectPath, legacyPath string, state *registryState) error {
	if state == nil {
		state = &registryState{}
	}
	normalizeRegistryState(state)
	if len(state.Projects) == 0 {
		if err := os.Remove(projectPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove worktree project registry: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
			return fmt.Errorf("create worktree project registry dir: %w", err)
		}
		payload, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("encode worktree project registry: %w", err)
		}
		tmpPath := projectPath + ".tmp"
		if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
			return fmt.Errorf("write worktree project registry: %w", err)
		}
		if err := os.Rename(tmpPath, projectPath); err != nil {
			return fmt.Errorf("replace worktree project registry: %w", err)
		}
	}
	if err := markLegacyRegistryDeprecated(legacyPath); err != nil {
		return err
	}
	return nil
}

func ensureProjectState(daemonCfg *config.DaemonConfig, project, mainPath string) error {
	project = strings.TrimSpace(project)
	mainPath = strings.TrimSpace(mainPath)
	if project == "" || mainPath == "" {
		return errors.New("project and main path are required")
	}
	projectPath, legacyPath, err := registryPaths(daemonCfg)
	if err != nil {
		return err
	}
	state, err := loadRegistryState(projectPath, legacyPath)
	if err != nil {
		return err
	}
	if setProjectMainPath(state, project, mainPath) {
		return saveRegistryState(projectPath, legacyPath, state)
	}
	return nil
}

// EnsureProjectStateForDir resolves the repository main path for dir and records
// the project in daemon state even if it has no registered non-main worktrees.
func EnsureProjectStateForDir(daemonCfg *config.DaemonConfig, dir string) (string, string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", "", errors.New("dir is required")
	}
	mainPath, err := ResolveMainPathInDir(dir)
	if err != nil {
		return "", "", err
	}
	project, err := resolveProjectIdentifierFromMainPath(mainPath)
	if err != nil {
		return "", "", err
	}
	if err := ensureProjectState(daemonCfg, project, mainPath); err != nil {
		return "", "", err
	}
	return project, mainPath, nil
}

func findProjectMainPath(daemonCfg *config.DaemonConfig, project string) (string, bool, error) {
	projectPath, legacyPath, err := registryPaths(daemonCfg)
	if err != nil {
		return "", false, err
	}
	state, err := loadRegistryState(projectPath, legacyPath)
	if err != nil {
		return "", false, err
	}
	for _, candidate := range state.Projects {
		if candidate.Name == strings.TrimSpace(project) && strings.TrimSpace(candidate.MainPath) != "" {
			return candidate.MainPath, true, nil
		}
	}
	return "", false, nil
}

func migrateLegacyRegistry(projectPath, legacyPath string) (*registryState, error) {
	legacy, exists, err := loadLegacyRegistryState(legacyPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &registryState{Projects: []projectState{}}, nil
	}
	state := convertLegacyRegistryState(legacy)
	if len(state.Projects) > 0 {
		if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
			return nil, fmt.Errorf("create worktree project registry dir: %w", err)
		}
		payload, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode worktree project registry: %w", err)
		}
		tmpPath := projectPath + ".tmp"
		if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
			return nil, fmt.Errorf("write worktree project registry: %w", err)
		}
		if err := os.Rename(tmpPath, projectPath); err != nil {
			return nil, fmt.Errorf("replace worktree project registry: %w", err)
		}
	}
	if err := writeLegacyRegistryDeprecated(legacyPath, legacy); err != nil {
		return nil, err
	}
	return state, nil
}

func loadLegacyRegistryState(path string) (*legacyRegistryState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read worktree registry: %w", err)
	}
	var state legacyRegistryState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("decode worktree registry: %w", err)
	}
	if state.Worktrees == nil {
		state.Worktrees = []Registration{}
	}
	return &state, true, nil
}

func convertLegacyRegistryState(legacy *legacyRegistryState) *registryState {
	state := &registryState{Projects: []projectState{}}
	if legacy == nil {
		return state
	}
	for _, entry := range legacy.Worktrees {
		upsertProjectWorktree(state, entry)
	}
	normalizeRegistryState(state)
	return state
}

func flattenRegistryState(state *registryState, project string) []Registration {
	if state == nil {
		return nil
	}
	entries := make([]Registration, 0)
	for _, candidate := range state.Projects {
		if project != "" && candidate.Name != project {
			continue
		}
		for _, wt := range candidate.Worktrees {
			entries = append(entries, Registration{
				Identifier: Identifier{Project: candidate.Name, Slug: wt.Slug},
				Path:       wt.Path,
				MainPath:   candidate.MainPath,
				Branch:     wt.Branch,
			})
		}
	}
	return entries
}

func upsertProjectWorktree(state *registryState, entry Registration) {
	project := ensureProjectEntry(state, entry.Project)
	if entry.MainPath != "" {
		project.MainPath = entry.MainPath
	}
	worktree := projectWorktreeRef{
		Slug:   entry.Slug,
		Path:   entry.Path,
		Branch: entry.Branch,
	}
	updated := false
	for i := range project.Worktrees {
		if project.Worktrees[i].Slug == entry.Slug {
			project.Worktrees[i] = worktree
			updated = true
			break
		}
	}
	if !updated {
		project.Worktrees = append(project.Worktrees, worktree)
	}
}

func removeProjectWorktree(state *registryState, project, slug string) {
	for i := range state.Projects {
		if state.Projects[i].Name != project {
			continue
		}
		filtered := state.Projects[i].Worktrees[:0]
		for _, wt := range state.Projects[i].Worktrees {
			if wt.Slug == slug {
				continue
			}
			filtered = append(filtered, wt)
		}
		state.Projects[i].Worktrees = filtered
		break
	}
	normalizeRegistryState(state)
}

func setProjectMainPath(state *registryState, project, mainPath string) bool {
	entry := ensureProjectEntry(state, project)
	if samePath(entry.MainPath, mainPath) {
		return false
	}
	entry.MainPath = mainPath
	return true
}

func ensureProjectEntry(state *registryState, project string) *projectState {
	if state.Projects == nil {
		state.Projects = []projectState{}
	}
	for i := range state.Projects {
		if state.Projects[i].Name == project {
			return &state.Projects[i]
		}
	}
	state.Projects = append(state.Projects, projectState{Name: project, Worktrees: []projectWorktreeRef{}})
	return &state.Projects[len(state.Projects)-1]
}

func normalizeRegistryState(state *registryState) {
	if state == nil {
		return
	}
	if state.Projects == nil {
		state.Projects = []projectState{}
	}
	projects := state.Projects[:0]
	for _, project := range state.Projects {
		project.Name = strings.TrimSpace(project.Name)
		project.MainPath = strings.TrimSpace(project.MainPath)
		if project.Name == "" {
			continue
		}
		if project.Worktrees == nil {
			project.Worktrees = []projectWorktreeRef{}
		}
		worktrees := project.Worktrees[:0]
		for _, wt := range project.Worktrees {
			wt.Slug = strings.TrimSpace(wt.Slug)
			wt.Path = strings.TrimSpace(wt.Path)
			wt.Branch = strings.TrimSpace(wt.Branch)
			if wt.Slug == "" || wt.Path == "" {
				continue
			}
			worktrees = append(worktrees, wt)
		}
		project.Worktrees = worktrees
		sort.Slice(project.Worktrees, func(i, j int) bool {
			return project.Worktrees[i].Slug < project.Worktrees[j].Slug
		})
		if project.MainPath == "" && len(project.Worktrees) == 0 {
			continue
		}
		projects = append(projects, project)
	}
	state.Projects = projects
	sort.Slice(state.Projects, func(i, j int) bool {
		return state.Projects[i].Name < state.Projects[j].Name
	})
}

func markLegacyRegistryDeprecated(path string) error {
	legacy, exists, err := loadLegacyRegistryState(path)
	if err != nil || !exists {
		return err
	}
	if legacy.Deprecated != nil {
		return nil
	}
	return writeLegacyRegistryDeprecated(path, legacy)
}

func writeLegacyRegistryDeprecated(path string, legacy *legacyRegistryState) error {
	if legacy == nil {
		legacy = &legacyRegistryState{}
	}
	if legacy.Worktrees == nil {
		legacy.Worktrees = []Registration{}
	}
	legacy.Deprecated = &legacyDeprecationState{
		Message:    "Deprecated; migrated to project-organized worktree state.",
		MigratedTo: projectRegistryFileName,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create worktree registry dir: %w", err)
	}
	payload, err := json.MarshalIndent(legacy, "", "  ")
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
