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

[process.db]
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

func TestLoadDaemonConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.toml")

	cfg, info, err := LoadDaemonConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.DaemonConfigFound {
		t.Fatalf("expected DaemonConfigFound=false")
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
}

func TestLoadDaemonConfig_GatewayFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.toml")
	worktreeDir := filepath.Join(dir, "worktrees")
	data := `
worktree_dir = "` + worktreeDir + `"

[gateway]
enabled = true
listen = ":443"
http_listen = ":80"
dns_zone = "tunnels.foocorp.dev"
hostname = "gw.foocorp.dev"
acme_email = "dev@foocorp.dev"
acme_directory = "https://acme-v02.api.letsencrypt.org/directory"
acme_storage = "~/gw-acme"
acme_resolvers = ["1.1.1.1", "8.8.8.8:53"]

[gateway.auth]
enabled = true
username = "foo"
password = "bar"

[gateway.route53]
enabled = true
hosted_zone_id = "Z123"
ttl = 60
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, info, err := LoadDaemonConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.DaemonConfigFound {
		t.Fatalf("expected DaemonConfigFound=true")
	}
	if !cfg.Gateway.Enabled || !cfg.Gateway.Auth.Enabled {
		t.Fatalf("unexpected gateway decode: %#v", cfg.Gateway)
	}
	if !cfg.Gateway.Route53.Enabled || cfg.Gateway.Route53.HostedZoneID != "Z123" || cfg.Gateway.DNSZone != "tunnels.foocorp.dev" || cfg.Gateway.Hostname != "gw.foocorp.dev" {
		t.Fatalf("unexpected route53/dns_zone decode: %#v", cfg.Gateway)
	}
	if cfg.Gateway.ACMEEmail == "" || cfg.Gateway.ACMEDir == "" || cfg.Gateway.ACMEStore == "" {
		t.Fatalf("expected acme fields to decode: %#v", cfg.Gateway)
	}
	if len(cfg.Gateway.ACMEResolvers) != 2 || cfg.Gateway.ACMEResolvers[0] != "1.1.1.1" {
		t.Fatalf("expected acme resolvers to decode: %#v", cfg.Gateway.ACMEResolvers)
	}
	if cfg.WorktreeDir != worktreeDir {
		t.Fatalf("expected worktree_dir %q, got %q", worktreeDir, cfg.WorktreeDir)
	}
}

func TestResolveGatewayDataDir(t *testing.T) {
	got, err := ResolveGatewayDataDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatalf("expected non-empty default data dir")
	}
}

func TestResolveWorktreeDir_Default(t *testing.T) {
	base := t.TempDir()
	old := os.Getenv("DEV_MODE_STATE_DIR")
	t.Cleanup(func() {
		_ = os.Setenv("DEV_MODE_STATE_DIR", old)
	})
	if err := os.Setenv("DEV_MODE_STATE_DIR", base); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	got, err := ResolveWorktreeDir(nil)
	if err != nil {
		t.Fatalf("ResolveWorktreeDir: %v", err)
	}
	want := filepath.Join(base, "worktrees")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveWorktreeDir_RequiresAbsolutePath(t *testing.T) {
	_, err := ResolveWorktreeDir(&DaemonConfig{WorktreeDir: "relative/path"})
	if err == nil {
		t.Fatalf("expected error for relative path")
	}
}
