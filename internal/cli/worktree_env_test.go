package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dev/internal/config"
)

func TestRunWorktreeEnv_MainWorktreeUsesConfiguredDNS(t *testing.T) {
	repoDir, daemonConfigPath, opts := setupWorktreeLifecycleRepo(t)
	projectConfig := `[project]
name = "demo"
main_slug = "primary"

[local-dns.overrides]
main = "demo"
`
	if err := os.WriteFile(filepath.Join(repoDir, config.DefaultProjectConfig), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonConfigPath, []byte(`[local-proxy]
apex_zone = ".stephen.test"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(repoDir, "tmp", "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	opts.WorkingDir = nestedDir
	opts.ConfigPath = config.DefaultProjectConfig
	opts.ResolvedPaths.ProjectConfig = filepath.Join(nestedDir, config.DefaultProjectConfig)

	var out bytes.Buffer
	if err := runWorktreeEnv(opts, "shell", &out); err != nil {
		t.Fatalf("runWorktreeEnv: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"export DEV_PROJECT='demo'\n",
		"export DEV_WORKTREE_SLUG='primary'\n",
		"export DEV_WORKTREE_DNS_NAME='demo.stephen.test'\n",
		"export DEV_WORKTREE_PATH='" + repoDir + "'\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected shell output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRunWorktreeEnv_LinkedWorktreeUsesRegisteredSlug(t *testing.T) {
	repoDir, daemonConfigPath, _ := setupWorktreeLifecycleRepo(t)
	linkedDir := filepath.Join(filepath.Dir(repoDir), "feature-worktree")
	if err := runGitForTest(repoDir, "worktree", "add", "-b", "feature/auth", linkedDir); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	opts := &config.GlobalFlags{
		WorkingDir: linkedDir,
		ResolvedPaths: config.ResolvedPaths{
			ProjectConfig: filepath.Join(linkedDir, config.DefaultProjectConfig),
			DaemonConfig:  daemonConfigPath,
		},
	}
	var registerOut, registerErr bytes.Buffer
	if err := runWorktreeRegister(opts, "feature/auth", &registerOut, &registerErr); err != nil {
		t.Fatalf("runWorktreeRegister: %v", err)
	}

	var out bytes.Buffer
	if err := runWorktreeEnv(opts, "json", &out); err != nil {
		t.Fatalf("runWorktreeEnv: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got["DEV_WORKTREE_SLUG"] != "feature/auth" {
		t.Fatalf("expected registered slug, got %q", got["DEV_WORKTREE_SLUG"])
	}
	if got["DEV_WORKTREE_DNS_NAME"] != "auth.localhost" {
		t.Fatalf("expected linked worktree DNS name, got %q", got["DEV_WORKTREE_DNS_NAME"])
	}
	if got["DEV_WORKTREE_PATH"] != linkedDir {
		t.Fatalf("expected linked worktree path, got %q", got["DEV_WORKTREE_PATH"])
	}
	if got["DEV_WORKTREE_BRANCH"] != "feature/auth" {
		t.Fatalf("expected linked branch, got %q", got["DEV_WORKTREE_BRANCH"])
	}
}

func TestShellQuote(t *testing.T) {
	value := "it's $(not-executed); safe\nnext line"
	quoted := shellQuote(value)
	cmd := exec.Command("sh", "-c", "value="+quoted+"; printf %s \"$value\"")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("evaluate quoted value: %v", err)
	}
	if got := string(output); got != value {
		t.Fatalf("evaluated value = %q, want %q", got, value)
	}
}
