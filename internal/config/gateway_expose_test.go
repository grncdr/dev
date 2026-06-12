package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayExposeModes_ParsesModesByProcess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = { mode = "rewrite" }, webpack = { mode = "reverse_proxy" } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeModes(cfg)
	if got["rails"] != GatewayModeRewrite {
		t.Fatalf("expected rails=%q, got %q", GatewayModeRewrite, got["rails"])
	}
	if got["webpack"] != GatewayModeReverseProxy {
		t.Fatalf("expected webpack=%q, got %q", GatewayModeReverseProxy, got["webpack"])
	}
}

func TestGatewayExposeModes_DefaultsModeToReverseProxy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = {} }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeModes(cfg)
	if got["rails"] != GatewayModeReverseProxy {
		t.Fatalf("expected rails=%q, got %q", GatewayModeReverseProxy, got["rails"])
	}
}

func TestGatewayExposeModes_InvalidModeDoesNotExpose(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = { mode = "wat" } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeModes(cfg)
	if _, ok := got["rails"]; ok {
		t.Fatalf("expected rails to be skipped for invalid mode")
	}
}

func TestGatewayExposeRules_ParsesDebugLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = { mode = "rewrite", debug_log = "logs/gateway-http.log" }, webpack = { mode = "reverse_proxy" } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeRules(cfg)
	if got["rails"].DebugLog != "logs/gateway-http.log" {
		t.Fatalf("expected rails debug_log=logs/gateway-http.log, got %q", got["rails"].DebugLog)
	}
	if got["rails"].Mode != GatewayModeRewrite {
		t.Fatalf("expected rails mode=%q, got %q", GatewayModeRewrite, got["rails"].Mode)
	}
	if got["webpack"].DebugLog != "" {
		t.Fatalf("expected webpack debug_log empty, got %q", got["webpack"].DebugLog)
	}
}

func TestGatewayExposeRules_ParsesAuthOptOut(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { webhooks = { mode = "reverse_proxy", auth = false }, rails = { mode = "rewrite", auth = true }, webpack = { mode = "reverse_proxy" } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeRules(cfg)
	if !got["webhooks"].NoAuth {
		t.Fatalf("expected webhooks NoAuth=true for auth=false")
	}
	if got["rails"].NoAuth {
		t.Fatalf("expected rails NoAuth=false for auth=true")
	}
	if got["webpack"].NoAuth {
		t.Fatalf("expected webpack NoAuth=false when auth omitted")
	}
}

func TestGatewayExposeRules_BareStringModeRequiresAuth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = "rewrite" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if GatewayExposeRules(cfg)["rails"].NoAuth {
		t.Fatalf("expected bare-string rule NoAuth=false")
	}
}

func TestGatewayExposeRules_ParsesRewritePeerSubdomains(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = { mode = "rewrite", rewrite_peer_subdomains = [" minio ", "API", ""] } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeRules(cfg)
	rule, ok := got["rails"]
	if !ok {
		t.Fatalf("expected rails expose rule")
	}
	want := []string{"minio", "api"}
	if len(rule.RewritePeerSubdomains) != len(want) {
		t.Fatalf("expected %d rewrite subdomains, got %d (%v)", len(want), len(rule.RewritePeerSubdomains), rule.RewritePeerSubdomains)
	}
	for i := range want {
		if rule.RewritePeerSubdomains[i] != want[i] {
			t.Fatalf("unexpected rewrite subdomains: got %v want %v", rule.RewritePeerSubdomains, want)
		}
	}
}

func TestGatewayExposeRules_ParsesWebSocketPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = { mode = "rewrite", websocket_paths = ["/cable", "cable/v2", "  ", "/live"] }, web = { mode = "reverse_proxy" } }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeRules(cfg)
	want := []string{"/cable", "/cable/v2", "/live"}
	if len(got["rails"].WebSocketPaths) != len(want) {
		t.Fatalf("unexpected websocket paths: got %v want %v", got["rails"].WebSocketPaths, want)
	}
	for i := range want {
		if got["rails"].WebSocketPaths[i] != want[i] {
			t.Fatalf("unexpected websocket paths: got %v want %v", got["rails"].WebSocketPaths, want)
		}
	}
	if got["web"].WebSocketPaths != nil {
		t.Fatalf("expected web WebSocketPaths nil when omitted, got %v", got["web"].WebSocketPaths)
	}
}

func TestGatewayExposeRules_BareStringModeHasNoWebSocketPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[gateway]
expose = { rails = "rewrite" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := GatewayExposeRules(cfg)["rails"].WebSocketPaths; got != nil {
		t.Fatalf("expected bare-string rule WebSocketPaths nil, got %v", got)
	}
}
