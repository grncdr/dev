package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type showPayloadTest struct {
	ProjectConfig map[string]any `json:"project_config"`
	UserConfig    map[string]any `json:"user_config"`
	Paths         struct {
		ProjectConfig     string `json:"project_config"`
		LocalOverride     string `json:"local_override"`
		LocalOverrideUsed bool   `json:"local_override_used"`
		UserConfig        string `json:"user_config"`
		UserConfigFound   bool   `json:"user_config_found"`
	} `json:"paths"`
}

func TestRunConfigShow_JSON(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".dev-mode.toml")
	userPath := filepath.Join(dir, "user.toml")

	project := `
[project]
name = "Foo Corp"
main_slug = "foocorp"
`
	if err := os.WriteFile(projectPath, []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	user := `
[worktrees."/abs/path"]
project = "Foo Corp"
trusted = true
`
	if err := os.WriteFile(userPath, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &Options{}
	opts.ResolvedPaths.ProjectConfig = projectPath
	opts.ResolvedPaths.UserConfig = userPath

	var buf bytes.Buffer
	if err := runConfigShow(opts, "json", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload showPayloadTest
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}

	if payload.Paths.ProjectConfig != projectPath {
		t.Fatalf("expected project path %q, got %q", projectPath, payload.Paths.ProjectConfig)
	}
	if payload.Paths.UserConfig != userPath {
		t.Fatalf("expected user path %q, got %q", userPath, payload.Paths.UserConfig)
	}

	projectBlock, ok := payload.ProjectConfig["project"].(map[string]any)
	if !ok {
		t.Fatalf("expected project block")
	}
	if projectBlock["name"] != "Foo Corp" {
		t.Fatalf("expected project.name 'Foo Corp', got %v", projectBlock["name"])
	}
}

func TestRunConfigShow_InvalidOutput(t *testing.T) {
	opts := &Options{}
	var buf bytes.Buffer
	if err := runConfigShow(opts, "yaml", &buf); err == nil {
		t.Fatalf("expected error for invalid output format")
	}
}
