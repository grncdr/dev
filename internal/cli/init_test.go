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
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	opts := &Options{
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

func TestRunInit_RefusesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.DefaultProjectConfig)
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &Options{
		ResolvedPaths: ResolvedPaths{
			ProjectConfig: path,
		},
	}

	var buf bytes.Buffer
	if err := runInit(opts, &buf); err == nil {
		t.Fatalf("expected error for existing config")
	}
}
