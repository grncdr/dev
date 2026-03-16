package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadProjectConfig_OverridesAndValidation(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, ".dev.toml")
	overridePath := filepath.Join(dir, ".dev.local.toml")

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
	basePath := filepath.Join(dir, ".dev.toml")

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

[global-hooks]
pre_worktree_add = "echo global-pre-add"
post_worktree_cleanup = "echo global-post-cleanup"

[project-hooks."demo/repo"]
post_worktree_add = "echo project-post-add"
pre_worktree_cleanup = "echo project-pre-cleanup"
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
	if cfg.GlobalHooks.PreWorktreeAdd != "echo global-pre-add" || cfg.GlobalHooks.PostWorktreeCleanup != "echo global-post-cleanup" {
		t.Fatalf("unexpected global hooks decode: %#v", cfg.GlobalHooks)
	}
	projectHooks, ok := cfg.ProjectHooks["demo/repo"]
	if !ok {
		t.Fatalf("expected project hooks entry for demo/repo")
	}
	if projectHooks.PostWorktreeAdd != "echo project-post-add" || projectHooks.PreWorktreeCleanup != "echo project-pre-cleanup" {
		t.Fatalf("unexpected project hooks decode: %#v", projectHooks)
	}
}

func TestResolveDaemonWorktreeLifecycleHooks(t *testing.T) {
	cfg := &DaemonConfig{
		GlobalHooks: HooksBlock{
			PreWorktreeAdd:      "echo global-pre-add",
			PostWorktreeCleanup: "echo global-post-cleanup",
		},
		ProjectHooks: map[string]HooksBlock{
			"demo/repo": {
				PreWorktreeAdd:      "echo project-pre-add",
				PostWorktreeCleanup: "echo project-post-cleanup",
			},
		},
	}
	hooks := ResolveDaemonWorktreeLifecycleHooks(cfg, "demo/repo")
	if len(hooks.PreWorktreeAdd) != 2 || hooks.PreWorktreeAdd[0] != "echo global-pre-add" || hooks.PreWorktreeAdd[1] != "echo project-pre-add" {
		t.Fatalf("unexpected pre_worktree_add hooks: %#v", hooks.PreWorktreeAdd)
	}
	if len(hooks.PostWorktreeCleanup) != 2 || hooks.PostWorktreeCleanup[0] != "echo global-post-cleanup" || hooks.PostWorktreeCleanup[1] != "echo project-post-cleanup" {
		t.Fatalf("unexpected post_worktree_cleanup hooks: %#v", hooks.PostWorktreeCleanup)
	}

	dupe := ResolveDaemonWorktreeLifecycleHooks(cfg, "demo/repo", "demo/repo")
	if len(dupe.PreWorktreeAdd) != 2 || len(dupe.PostWorktreeCleanup) != 2 {
		t.Fatalf("expected duplicate project keys to be deduplicated, got %#v", dupe)
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
}

func TestProjectGatewayURL(t *testing.T) {
	cfg := &ProjectConfig{Gateway: map[string]any{"url": " https://gw.example.test "}}
	if got := ProjectGatewayURL(cfg); got != "https://gw.example.test" {
		t.Fatalf("expected gateway url, got %q", got)
	}
}

func TestProjectGatewayAuthCredentials(t *testing.T) {
	cfg := &ProjectConfig{
		Gateway: map[string]any{
			"auth": map[string]any{
				"username": "alice",
				"password": "secret",
			},
		},
	}
	creds, enabled, err := ProjectGatewayAuthCredentials(cfg)
	if err != nil {
		t.Fatalf("ProjectGatewayAuthCredentials: %v", err)
	}
	if !enabled {
		t.Fatalf("expected auth defaults to be enabled")
	}
	if creds.Username != "alice" || creds.Password != "secret" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}
}

func TestProjectGatewayAuthCredentials_RequiresBothFields(t *testing.T) {
	cfg := &ProjectConfig{
		Gateway: map[string]any{
			"auth": map[string]any{
				"username": "alice",
			},
		},
	}
	_, _, err := ProjectGatewayAuthCredentials(cfg)
	if err == nil {
		t.Fatalf("expected incomplete auth config error")
	}
}

func TestResolveGatewayCredentialDir(t *testing.T) {
	base := t.TempDir()
	cfg := &DaemonConfig{StateDir: base}
	got, err := ResolveGatewayCredentialDir(cfg, "gw.example.test")
	if err != nil {
		t.Fatalf("ResolveGatewayCredentialDir: %v", err)
	}
	want := filepath.Join(base, "gateway", "agent-credentials", "gw.example.test")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveGatewayCredentialDirForURL(t *testing.T) {
	base := t.TempDir()
	cfg := &DaemonConfig{StateDir: base}
	got, err := ResolveGatewayCredentialDirForURL(cfg, "https://gw.example.test:8443/path")
	if err != nil {
		t.Fatalf("ResolveGatewayCredentialDirForURL: %v", err)
	}
	want := filepath.Join(base, "gateway", "agent-credentials", "gw.example.test")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestGatewayCredentialHelpers(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "renew-me"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(2 * time.Hour),
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	err = WriteGatewayCredentials(dir, GatewayCredentialMaterial{
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		CAPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	})
	if err != nil {
		t.Fatalf("WriteGatewayCredentials: %v", err)
	}
	info, err := LoadGatewayCredentialInfo(dir)
	if err != nil {
		t.Fatalf("LoadGatewayCredentialInfo: %v", err)
	}
	if info.CommonName != "renew-me" {
		t.Fatalf("expected CN renew-me, got %q", info.CommonName)
	}
	if !info.ExpiresAt.After(time.Now()) {
		t.Fatalf("expected future expiry, got %s", info.ExpiresAt)
	}
}

func TestExpandCommandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := ExpandCommandPath([]string{"~/bin/hook", "--flag"})
	if err != nil {
		t.Fatalf("ExpandCommandPath: %v", err)
	}
	want := []string{filepath.Join(home, "bin", "hook"), "--flag"}
	if len(got) != len(want) {
		t.Fatalf("unexpected command length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCollapseUserPath(t *testing.T) {
	home := t.TempDir()
	outsideHome := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "home directory",
			path: home,
			want: "~",
		},
		{
			name: "path under home",
			path: filepath.Join(home, "src", "repo"),
			want: filepath.Join("~", "src", "repo"),
		},
		{
			name: "path outside home",
			path: filepath.Join(outsideHome, "repo"),
			want: filepath.Join(outsideHome, "repo"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CollapseUserPath(tt.path); got != tt.want {
				t.Fatalf("CollapseUserPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestCollapseUserPath_HandlesHomeSymlinkDifferences(t *testing.T) {
	base := t.TempDir()
	homeReal := filepath.Join(base, "home-real")
	homeLink := filepath.Join(base, "home-link")
	if err := os.MkdirAll(filepath.Join(homeReal, "src"), 0o755); err != nil {
		t.Fatalf("mkdir home real: %v", err)
	}
	if err := os.Symlink(homeReal, homeLink); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	t.Setenv("HOME", homeLink)

	worktreePath := filepath.Join(homeReal, "src", "repo")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree path: %v", err)
	}
	got := CollapseUserPath(worktreePath)
	want := filepath.Join("~", "src", "repo")
	if got != want {
		t.Fatalf("CollapseUserPath with symlinked home = %q, want %q", got, want)
	}
}

func TestResolveWorktreeDir_Default(t *testing.T) {
	base := t.TempDir()
	old := os.Getenv("DEV_STATE_DIR")
	t.Cleanup(func() {
		_ = os.Setenv("DEV_STATE_DIR", old)
	})
	if err := os.Setenv("DEV_STATE_DIR", base); err != nil {
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

func TestResolveStateDir_FromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &DaemonConfig{StateDir: dir}
	got, err := ResolveStateDir(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Fatalf("expected %q, got %q", dir, got)
	}
}

func TestResolveStateDir_RequiresAbsolutePath(t *testing.T) {
	cfg := &DaemonConfig{StateDir: "relative/path"}
	_, err := ResolveStateDir(cfg)
	if err == nil {
		t.Fatalf("expected error for relative path")
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   int
		wantOK bool
	}{
		{name: "int", input: 42, want: 42, wantOK: true},
		{name: "int64", input: int64(100), want: 100, wantOK: true},
		{name: "uint", input: uint(50), want: 50, wantOK: true},
		{name: "uint64", input: uint64(200), want: 200, wantOK: true},
		{name: "float64", input: float64(3.7), want: 3, wantOK: true},
		{name: "float32", input: float32(2.5), want: 2, wantOK: true},
		{name: "string", input: "123", want: 0, wantOK: false},
		{name: "nil", input: nil, want: 0, wantOK: false},
		{name: "bool", input: true, want: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseInt(tt.input)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ParseInt(%v) = (%d, %v), want (%d, %v)", tt.input, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
