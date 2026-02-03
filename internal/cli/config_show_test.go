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
	DaemonConfig  map[string]any `json:"daemon_config"`
	Paths         struct {
		ProjectConfig     string `json:"project_config"`
		LocalOverride     string `json:"local_override"`
		LocalOverrideUsed bool   `json:"local_override_used"`
		DaemonConfig      string `json:"daemon_config"`
		DaemonConfigFound bool   `json:"daemon_config_found"`
	} `json:"paths"`
}

func TestRunConfigShow_JSON(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".dev-mode.toml")
	daemonPath := filepath.Join(dir, "daemon.toml")

	project := `
[project]
name = "Foo Corp"
main_slug = "foocorp"
`
	if err := os.WriteFile(projectPath, []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}

	daemon := `
[proxy]
apex_zone = ".localhost"
`
	if err := os.WriteFile(daemonPath, []byte(daemon), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := &Options{}
	opts.ResolvedPaths.ProjectConfig = projectPath
	opts.ResolvedPaths.DaemonConfig = daemonPath

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
	if payload.Paths.DaemonConfig != daemonPath {
		t.Fatalf("expected daemon path %q, got %q", daemonPath, payload.Paths.DaemonConfig)
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
