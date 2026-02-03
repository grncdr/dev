package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"dev-mode/internal/config"
)

func TestRunInit_WritesConfig(t *testing.T) {
	dir := t.TempDir()
	orig := rememberCWD()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	opts := &Options{
		WorkingDir: dir,
		ResolvedPaths: ResolvedPaths{
			ProjectConfig: filepath.Join(dir, config.DefaultProjectConfig),
		},
	}

	var buf bytes.Buffer
	if err := runInit(opts, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(opts.ResolvedPaths.ProjectConfig)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.ProjectConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if cfg.Project.Name != filepath.Base(dir) {
		t.Fatalf("expected project.name %q, got %q", filepath.Base(dir), cfg.Project.Name)
	}

	if !strings.Contains(string(data), "# [hooks]") {
		t.Fatalf("expected commented hooks section")
	}
}

func TestRunInit_UsesOriginRemoteOwnerRepo(t *testing.T) {
	dir := t.TempDir()
	if err := runGitForTest(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runGitForTest(dir, "remote", "add", "origin", "git@github.com:FooCorp/Monorepo.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	orig := rememberCWD()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()

	opts := &Options{
		WorkingDir: dir,
		ResolvedPaths: ResolvedPaths{
			ProjectConfig: filepath.Join(dir, config.DefaultProjectConfig),
		},
	}
	var buf bytes.Buffer
	if err := runInit(opts, &buf); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	data, err := os.ReadFile(opts.ResolvedPaths.ProjectConfig)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.ProjectConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Project.Name != "foocorp/monorepo" {
		t.Fatalf("expected remote-derived project name, got %q", cfg.Project.Name)
	}
}

func TestParseOwnerRepoFromRemoteURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"git@github.com:FooCorp/Monorepo.git", "foocorp/monorepo", true},
		{"https://github.com/FooCorp/Monorepo.git", "foocorp/monorepo", true},
		{"ssh://git@github.com/FooCorp/Monorepo.git", "foocorp/monorepo", true},
		{"https://github.com/FooCorp", "", false},
		{"/tmp/local/repo", "", false},
	}
	for _, tc := range cases {
		got, ok := parseOwnerRepoFromRemoteURL(tc.raw)
		if ok != tc.ok {
			t.Fatalf("parseOwnerRepoFromRemoteURL(%q) ok=%v want %v", tc.raw, ok, tc.ok)
		}
		if got != tc.want {
			t.Fatalf("parseOwnerRepoFromRemoteURL(%q)=%q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestRunInit_RefusesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultProjectConfig)
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &Options{
		WorkingDir: dir,
		ResolvedPaths: ResolvedPaths{
			ProjectConfig: path,
		},
	}

	var buf bytes.Buffer
	if err := runInit(opts, &buf); err == nil {
		t.Fatalf("expected error for existing config")
	}
}
