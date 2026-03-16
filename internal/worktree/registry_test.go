package worktree

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dev/internal/config"
)

func TestListRegisteredWorktreesMigratesLegacyRegistry(t *testing.T) {
	stateDir := t.TempDir()
	legacyPath := filepath.Join(stateDir, legacyWorktreeRegistryFileName)
	legacy := `{
  "worktrees": [
    {
      "project": "demo",
      "slug": "feature",
      "path": "/tmp/demo-feature",
      "main_path": "/tmp/demo-main",
      "branch": "feature"
    }
  ]
}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy registry: %v", err)
	}

	daemonCfg := &config.DaemonConfig{StateDir: stateDir}
	entries, err := ListRegisteredWorktrees(daemonCfg, "demo")
	if err != nil {
		t.Fatalf("ListRegisteredWorktrees: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 migrated entry, got %d", len(entries))
	}
	if entries[0].Project != "demo" || entries[0].Slug != "feature" {
		t.Fatalf("unexpected migrated entry: %+v", entries[0])
	}

	projectData, err := os.ReadFile(filepath.Join(stateDir, projectRegistryFileName))
	if err != nil {
		t.Fatalf("read project registry: %v", err)
	}
	var projectState registryState
	if err := json.Unmarshal(projectData, &projectState); err != nil {
		t.Fatalf("decode project registry: %v", err)
	}
	if len(projectState.Projects) != 1 {
		t.Fatalf("expected 1 project in migrated registry, got %d", len(projectState.Projects))
	}
	if projectState.Projects[0].Name != "demo" || projectState.Projects[0].MainPath != "/tmp/demo-main" {
		t.Fatalf("unexpected project registry state: %+v", projectState.Projects[0])
	}

	legacyData, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read legacy registry: %v", err)
	}
	var deprecated legacyRegistryState
	if err := json.Unmarshal(legacyData, &deprecated); err != nil {
		t.Fatalf("decode legacy registry: %v", err)
	}
	if deprecated.Deprecated == nil {
		t.Fatalf("expected legacy registry to be marked deprecated")
	}
	if deprecated.Deprecated.MigratedTo != projectRegistryFileName {
		t.Fatalf("expected migrated_to %q, got %q", projectRegistryFileName, deprecated.Deprecated.MigratedTo)
	}
}

func TestResolvePathFromSlugWithRegistrySeedsMainProjectState(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForWorktreeTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte("[project]\nname=\"demo\"\nmain_slug=\"primary\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForWorktreeTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForWorktreeTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	daemonCfg := &config.DaemonConfig{StateDir: filepath.Join(base, "state")}
	got, err := ResolvePathFromSlugWithRegistry("primary", repoDir, daemonCfg)
	if err != nil {
		t.Fatalf("ResolvePathFromSlugWithRegistry: %v", err)
	}
	if !samePath(got, repoDir) {
		t.Fatalf("expected %s, got %s", repoDir, got)
	}

	resolved, err := ResolvePathFromProjectSlug("demo", "primary", daemonCfg)
	if err != nil {
		t.Fatalf("ResolvePathFromProjectSlug: %v", err)
	}
	if !samePath(resolved, repoDir) {
		t.Fatalf("expected %s, got %s", repoDir, resolved)
	}

	projectData, err := os.ReadFile(filepath.Join(base, "state", projectRegistryFileName))
	if err != nil {
		t.Fatalf("read project registry: %v", err)
	}
	var projectState registryState
	if err := json.Unmarshal(projectData, &projectState); err != nil {
		t.Fatalf("decode project registry: %v", err)
	}
	if len(projectState.Projects) != 1 {
		t.Fatalf("expected 1 project entry, got %d", len(projectState.Projects))
	}
	if !samePath(projectState.Projects[0].MainPath, repoDir) {
		t.Fatalf("expected main path %q, got %q", repoDir, projectState.Projects[0].MainPath)
	}
	if len(projectState.Projects[0].Worktrees) != 0 {
		t.Fatalf("expected no registered worktrees for main-only repo, got %+v", projectState.Projects[0].Worktrees)
	}
}

func runGitForWorktreeTest(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dev",
		"GIT_AUTHOR_EMAIL=dev@example.com",
		"GIT_COMMITTER_NAME=dev",
		"GIT_COMMITTER_EMAIL=dev@example.com",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return worktreeGitTestError(strings.TrimSpace(string(output)))
	}
	return nil
}

type worktreeGitTestError string

func (e worktreeGitTestError) Error() string {
	return string(e)
}
