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
	mainSlug := filepath.Base(repoDir)
	target := mainSlug + ":feature"

	if err := runGitForTest(repoDir, "branch", "feature"); err != nil {
		t.Fatalf("create branch: %v", err)
	}

	var addOut bytes.Buffer
	var addErr bytes.Buffer
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(repoDir)
		_ = os.Remove(daemonConfigPath)
	})
	if err := runWorktreeAdd(opts, target, "", &addOut, &addErr); err != nil {
		t.Fatalf("runWorktreeAdd: %v", err)
	}
	if !strings.Contains(addErr.String(), "already exists; reusing") {
		t.Fatalf("expected branch reuse warning, got %q", addErr.String())
	}

	worktreePath := filepath.Join(filepath.Dir(daemonConfigPath), "managed", mainSlug, "feature")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("expected worktree at %s: %v", worktreePath, err)
	}

	var listOut bytes.Buffer
	if err := runWorktreeList(opts, &listOut); err != nil {
		t.Fatalf("runWorktreeList: %v", err)
	}
	if !strings.Contains(listOut.String(), mainSlug+":feature") {
		t.Fatalf("expected list output to include feature worktree, got %q", listOut.String())
	}

	subdir := filepath.Join(worktreePath, "tmp")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}

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
	return repoDir, daemonConfigPath, &Options{ResolvedPaths: ResolvedPaths{DaemonConfig: daemonConfigPath}}
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
