package cli

import (
	"os"
	"path/filepath"
	"testing"

	"dev/internal/config"
	"dev/internal/worktree"
)

func TestResolveProcessTargetsExplicit(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"proj:feature:rails",
		"proj:feature:webpack",
		"proj:other:*",
	}, nil)
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(targets))
	}
	feature := targets["feature"]
	if feature == nil || feature.all {
		t.Fatalf("expected specific processes for feature")
	}
	if feature.project != "proj" {
		t.Fatalf("expected project proj, got %q", feature.project)
	}
	list := feature.processList()
	if len(list) != 2 || list[0] != "rails" || list[1] != "webpack" {
		t.Fatalf("unexpected process list: %+v", list)
	}
	other := targets["other"]
	if other == nil || !other.all {
		t.Fatalf("expected wildcard target for other")
	}
}

func TestResolveProcessTargetsConflictingProjectQualifiers(t *testing.T) {
	_, err := resolveProcessTargets([]string{
		"proj-a:feature:rails",
		"proj-b:feature:worker",
	}, nil)
	if err == nil {
		t.Fatalf("expected conflict error")
	}
}

func TestResolveProcessTargetsWildcardWins(t *testing.T) {
	targets, err := resolveProcessTargets([]string{
		"foo:rails",
		"foo:*",
		"foo:webpack",
	}, nil)
	if err != nil {
		t.Fatalf("resolveProcessTargets: %v", err)
	}
	target := targets["foo"]
	if target == nil {
		t.Fatalf("expected target foo")
	}
	if !target.all {
		t.Fatalf("expected wildcard all=true")
	}
	if target.processList() != nil {
		t.Fatalf("expected no specific process list when wildcard is set")
	}
}

func TestResolveStopProcessTargetsProjectWildcardSlug(t *testing.T) {
	base := t.TempDir()
	mainPath := filepath.Join(base, "repo")
	featurePath := filepath.Join(base, "feature")
	bugfixPath := filepath.Join(base, "bugfix")
	if err := os.MkdirAll(mainPath, 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	if err := os.MkdirAll(featurePath, 0o755); err != nil {
		t.Fatalf("mkdir feature: %v", err)
	}
	if err := os.MkdirAll(bugfixPath, 0o755); err != nil {
		t.Fatalf("mkdir bugfix: %v", err)
	}

	cfg := "[project]\nname=\"demo\"\nmain_slug=\"primary\"\n"
	if err := os.WriteFile(filepath.Join(mainPath, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "state_dir = \"" + filepath.Join(base, "state") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	daemonCfg, _, err := config.LoadDaemonConfig(daemonConfigPath)
	if err != nil {
		t.Fatalf("load daemon config: %v", err)
	}

	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project:  "demo",
		Slug:     "feature",
		Path:     featurePath,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register feature: %v", err)
	}
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project:  "demo",
		Slug:     "bugfix",
		Path:     bugfixPath,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register bugfix: %v", err)
	}

	opts := &Options{
		WorkingDir: base,
		ResolvedPaths: ResolvedPaths{
			DaemonConfig: daemonConfigPath,
		},
	}
	targets, err := resolveStopProcessTargets([]string{"demo:*:*"}, opts)
	if err != nil {
		t.Fatalf("resolveStopProcessTargets: %v", err)
	}

	for _, slug := range []string{"feature", "bugfix", "primary"} {
		target := targets[slug]
		if target == nil {
			t.Fatalf("expected target for slug %q", slug)
		}
		if target.project != "demo" {
			t.Fatalf("expected project demo for %q, got %q", slug, target.project)
		}
		if !target.all {
			t.Fatalf("expected all=true for %q", slug)
		}
	}
}

func TestResolveStopProcessTargetsProjectWildcardSlugSpecificProcess(t *testing.T) {
	base := t.TempDir()
	mainPath := filepath.Join(base, "repo")
	featurePath := filepath.Join(base, "feature")
	if err := os.MkdirAll(mainPath, 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	if err := os.MkdirAll(featurePath, 0o755); err != nil {
		t.Fatalf("mkdir feature: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mainPath, ".dev.toml"), []byte("[project]\nname=\"demo\"\n"), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "state_dir = \"" + filepath.Join(base, "state") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	daemonCfg, _, err := config.LoadDaemonConfig(daemonConfigPath)
	if err != nil {
		t.Fatalf("load daemon config: %v", err)
	}
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project:  "demo",
		Slug:     "feature",
		Path:     featurePath,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register feature: %v", err)
	}

	opts := &Options{
		WorkingDir: base,
		ResolvedPaths: ResolvedPaths{
			DaemonConfig: daemonConfigPath,
		},
	}
	targets, err := resolveStopProcessTargets([]string{"demo:*:worker"}, opts)
	if err != nil {
		t.Fatalf("resolveStopProcessTargets: %v", err)
	}
	for _, slug := range []string{"feature", "main"} {
		target := targets[slug]
		if target == nil {
			t.Fatalf("expected target for slug %q", slug)
		}
		if target.all {
			t.Fatalf("expected all=false for %q", slug)
		}
		list := target.processList()
		if len(list) != 1 || list[0] != "worker" {
			t.Fatalf("unexpected process list for %q: %+v", slug, list)
		}
	}
}

func TestResolveStopProcessTargetsProjectWildcardNoMatches(t *testing.T) {
	base := t.TempDir()
	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "state_dir = \"" + filepath.Join(base, "state") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	opts := &Options{
		WorkingDir: base,
		ResolvedPaths: ResolvedPaths{
			DaemonConfig: daemonConfigPath,
		},
	}

	_, err := resolveStopProcessTargets([]string{"demo:*:*"}, opts)
	if err == nil {
		t.Fatalf("expected error for missing project worktrees")
	}
}

func TestResolveSlugProjectQualified(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte("[project]\nname=\"demo\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	orig := rememberCWD()
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	slug, err := resolveSlug(&Options{WorkingDir: repoDir}, "demo:feature/x")
	if err != nil {
		t.Fatalf("resolveSlug: %v", err)
	}
	if slug != "feature/x" {
		t.Fatalf("expected feature/x, got %q", slug)
	}
}

func TestResolveSlugUsesRegisteredSlugForCurrentWorktree(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte("[project]\nname=\"demo\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	worktreePath := filepath.Join(base, "outside", "feature-dir")
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := runGitForTest(repoDir, "worktree", "add", "-b", "feature/something", worktreePath, "HEAD"); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	daemonConfigPath := filepath.Join(base, "daemon.toml")
	stateDir := filepath.Join(base, "state")
	daemonConfig := "state_dir = \"" + stateDir + "\"\nworktree_dir = \"" + filepath.Join(base, "managed") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}

	daemonCfg, _, err := config.LoadDaemonConfig(daemonConfigPath)
	if err != nil {
		t.Fatalf("load daemon config: %v", err)
	}
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project: "demo",
		Slug:    "feature/something",
		Path:    worktreePath,
	}); err != nil {
		t.Fatalf("register worktree in state: %v", err)
	}

	slug, err := resolveSlug(&Options{WorkingDir: worktreePath, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}, "")
	if err != nil {
		t.Fatalf("resolveSlug: %v", err)
	}
	if slug != "feature/something" {
		t.Fatalf("expected registered slug, got %q", slug)
	}
}

func TestResolveSlugDefaultsToMainForMainWorktree(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte("[project]\nname=\"demo\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "state_dir = \"" + filepath.Join(base, "state") + "\"\nworktree_dir = \"" + filepath.Join(base, "managed") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}

	slug, err := resolveSlug(&Options{WorkingDir: repoDir, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}, "")
	if err != nil {
		t.Fatalf("resolveSlug: %v", err)
	}
	if slug != "main" {
		t.Fatalf("expected main, got %q", slug)
	}
}

func TestResolveSlugUsesConfiguredMainSlugForMainWorktree(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfg := "[project]\nname=\"demo\"\nmain_slug=\"primary\"\n"
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := runGitForTest(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGitForTest(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "state_dir = \"" + filepath.Join(base, "state") + "\"\nworktree_dir = \"" + filepath.Join(base, "managed") + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}

	slug, err := resolveSlug(&Options{WorkingDir: repoDir, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}, "")
	if err != nil {
		t.Fatalf("resolveSlug: %v", err)
	}
	if slug != "primary" {
		t.Fatalf("expected primary, got %q", slug)
	}

	daemonCfg, _, err := config.LoadDaemonConfig(daemonConfigPath)
	if err != nil {
		t.Fatalf("load daemon config: %v", err)
	}
	path, err := worktree.ResolvePathFromSlugWithRegistry("primary", repoDir, daemonCfg)
	if err != nil {
		t.Fatalf("ResolvePathFromSlugWithRegistry: %v", err)
	}
	if !samePath(path, repoDir) {
		t.Fatalf("expected path %s, got %s", repoDir, path)
	}
}
