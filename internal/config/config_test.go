package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectConfig_OverridesAndValidation(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, ".dev-mode.toml")
	overridePath := filepath.Join(dir, ".dev-mode.local.toml")

	base := `
[project]
name = "Foo Corp"

[processes.db]
singleton = true
`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}

	override := `
[project]
name = "override"
`
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, info, err := LoadProjectConfig(basePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "override" {
		t.Fatalf("expected override name, got %q", cfg.Project.Name)
	}
	if !info.LocalOverrideUsed {
		t.Fatalf("expected local override to be used")
	}
}

func TestLoadProjectConfig_RequiresFields(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, ".dev-mode.toml")

	base := `
[project]
name = ""
`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadProjectConfig(basePath)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestLoadUserConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.toml")

	cfg, info, err := LoadUserConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UserConfigFound {
		t.Fatalf("expected UserConfigFound=false")
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
}

func TestLoadUserConfig_GatewayFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.toml")
	data := `
[gateway]
enabled = true
data_dir = "~/gw"
listen = ":443"
http_listen = ":80"

[gateway.auth]
enabled = true
username = "foo"
password = "bar"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, info, err := LoadUserConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.UserConfigFound {
		t.Fatalf("expected UserConfigFound=true")
	}
	if !cfg.Gateway.Enabled || cfg.Gateway.DataDir != "~/gw" || !cfg.Gateway.Auth.Enabled {
		t.Fatalf("unexpected gateway decode: %#v", cfg.Gateway)
	}
}

func TestResolveGatewayDataDir(t *testing.T) {
	got, err := ResolveGatewayDataDir(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("expected non-empty default data dir")
	}

	cfg := &UserConfig{}
	cfg.Gateway.DataDir = "~/my-gateway"
	got, err = ResolveGatewayDataDir(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(got) != "my-gateway" {
		t.Fatalf("expected expanded custom dir, got %q", got)
	}
}
