package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeLifecycleAddListCleanupDryRun(t *testing.T) {
	repoDir, daemonConfigPath, opts := setupWorktreeLifecycleRepo(t)
	projectID := "demo"
	target := "feature"
	origWD := rememberCWD()
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	if err := runGitForTest(repoDir, "branch", "feature"); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	var addOut bytes.Buffer
	var addErr bytes.Buffer
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(daemonConfigPath)
	})
	if err := runWorktreeAdd(opts, target, "", &addOut, &addErr); err != nil {
		t.Fatalf("runWorktreeAdd: %v", err)
	}
	if !strings.Contains(addErr.String(), "already exists; reusing") {
		t.Fatalf("expected branch reuse warning, got %q", addErr.String())
	}

	worktreePath := filepath.Join(filepath.Dir(daemonConfigPath), "managed", projectID, "feature")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree at %s: %v", worktreePath, err)
	}

	var listOut bytes.Buffer
	if err := runWorktreeList(opts, &listOut); err != nil {
		t.Fatalf("runWorktreeList: %v", err)
	}
	if !strings.Contains(listOut.String(), projectID+":feature") {
		t.Fatalf("expected list output to include feature worktree, got %q", listOut.String())
	}

	subdir := filepath.Join(worktreePath, "tmp")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}
	opts.WorkingDir = subdir

	var cleanupOut bytes.Buffer
	if err := runWorktreeCleanup(opts, "", &worktreeCleanupOptions{DryRun: true, Force: true}, &cleanupOut, &addErr); err != nil {
		t.Fatalf("runWorktreeCleanup dry-run: %v", err)
	}
	if !strings.Contains(cleanupOut.String(), "would remove worktree") {
		t.Fatalf("expected dry-run output, got %q", cleanupOut.String())
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree to remain after dry-run: %v", err)
	}
}

func TestWorktreeCleanupMainFails(t *testing.T) {
	repoDir, _, opts := setupWorktreeLifecycleRepo(t)
	origWD := rememberCWD()
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runWorktreeCleanup(opts, "", &worktreeCleanupOptions{}, &out, &errBuf)
	if err == nil {
		t.Fatalf("expected cleanup to fail for main worktree")
	}
	if !strings.Contains(err.Error(), "main worktree") {
		t.Fatalf("expected main worktree error, got %v", err)
	}
}

func TestWorktreeLifecycleHooksIncludeLocalDNSName(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	configPath := filepath.Join(repoDir, ".dev-mode.toml")
	projectConfig := `
[project]
name = "demo"

[commands]
wrapper = "env DEV_MODE_WRAPPED=1 $COMMAND"

[hooks]
pre_worktree_add = "sh -c \"echo ${DEV_MODE_WRAPPED}:${DEV_MODE_WORKTREE_DNS_NAME} > hook_pre_add.txt\""
post_worktree_add = "sh -c \"echo ${DEV_MODE_WRAPPED}:${DEV_MODE_WORKTREE_DNS_NAME} > hook_post_add.txt\""
pre_worktree_cleanup = "sh -c \"echo ${DEV_MODE_WRAPPED}:${DEV_MODE_WORKTREE_DNS_NAME} > hook_pre_cleanup.txt\""
post_worktree_cleanup = "sh -c \"echo ${DEV_MODE_WRAPPED}:${DEV_MODE_WORKTREE_DNS_NAME} > hook_post_cleanup.txt\""
`
	if err := os.WriteFile(configPath, []byte(projectConfig), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
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

	worktreeRoot := filepath.Join(base, "managed")
	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "worktree_dir = \"" + worktreeRoot + "\"\n\n[local-proxy]\napex_zone = \".dev.test\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	opts := &Options{WorkingDir: repoDir, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}

	origWD := rememberCWD()
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	target := "feature"
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := runWorktreeAdd(opts, target, "", &out, &errOut); err != nil {
		t.Fatalf("runWorktreeAdd: %v", err)
	}

	worktreePath := filepath.Join(worktreeRoot, "demo", "feature")
	for _, path := range []string{
		filepath.Join(repoDir, "hook_pre_add.txt"),
		filepath.Join(worktreePath, "hook_post_add.txt"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.TrimSpace(string(data)) != "1:feature.dev.test" {
			t.Fatalf("expected wrapped dns in %s, got %q", path, strings.TrimSpace(string(data)))
		}
	}

	if err := runWorktreeCleanup(opts, target, &worktreeCleanupOptions{Force: true}, &out, &errOut); err != nil {
		t.Fatalf("runWorktreeCleanup: %v", err)
	}
	for _, path := range []string{
		filepath.Join(repoDir, "hook_post_cleanup.txt"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.TrimSpace(string(data)) != "1:feature.dev.test" {
			t.Fatalf("expected wrapped dns in %s, got %q", path, strings.TrimSpace(string(data)))
		}
	}
}

func TestWorktreeLifecycleProjectAndSlugWithSlashes(t *testing.T) {
	base := t.TempDir()
	repoDir := filepath.Join(base, "foocorp", "monorepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	projectConfig := `
[project]
name = "foocorp/monorepo"
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev-mode.toml"), []byte(projectConfig), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
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

	worktreeRoot := filepath.Join(base, "managed")
	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "worktree_dir = \"" + worktreeRoot + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	opts := &Options{WorkingDir: repoDir, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}
	origWD := rememberCWD()
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	target := "foocorp/monorepo:feature/something"
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := runWorktreeAdd(opts, target, "", &out, &errOut); err != nil {
		t.Fatalf("runWorktreeAdd: %v", err)
	}

	worktreePath := filepath.Join(worktreeRoot, "foocorp", "monorepo", "feature", "something")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected nested worktree path %s: %v", worktreePath, err)
	}

	if err := runWorktreeCleanup(opts, target, &worktreeCleanupOptions{Force: true}, &out, &errOut); err != nil {
		t.Fatalf("runWorktreeCleanup: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree cleanup to remove %s", worktreePath)
	}
}

func setupWorktreeLifecycleRepo(t *testing.T) (string, string, *Options) {
	t.Helper()
	base := t.TempDir()
	repoDir := filepath.Join(base, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := runGitForTest(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	configPath := filepath.Join(repoDir, ".dev-mode.toml")
	projectConfig := `
[project]
name = "demo"
`
	if err := os.WriteFile(configPath, []byte(projectConfig), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
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

	worktreeRoot := filepath.Join(base, "managed")
	daemonConfigPath := filepath.Join(base, "daemon.toml")
	daemonConfig := "worktree_dir = \"" + worktreeRoot + "\"\n"
	if err := os.WriteFile(daemonConfigPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
	return repoDir, daemonConfigPath, &Options{WorkingDir: repoDir, ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}
}

func runGitForTest(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=dev-mode",
		"GIT_AUTHOR_EMAIL=dev-mode@example.com",
		"GIT_COMMITTER_NAME=dev-mode",
		"GIT_COMMITTER_EMAIL=dev-mode@example.com",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			return err
		}
		return &gitTestError{msg: strings.TrimSpace(string(output))}
	}
	return nil
}

type gitTestError struct {
	msg string
}

func (e *gitTestError) Error() string {
	return e.msg
}
