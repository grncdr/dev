package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayExposeModes_ParsesModesByProcess(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
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
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
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
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
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
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
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
