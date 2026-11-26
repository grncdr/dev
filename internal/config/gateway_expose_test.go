package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestGatewayExposeRules_ParsesWebSocketPathsAsPublicPaths(t *testing.T) {
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
	want := PublicPaths{"/cable": {WebSocket: true}, "/cable/v2": {WebSocket: true}, "/live": {WebSocket: true}}
	if !reflect.DeepEqual(got["rails"].PublicPaths, want) {
		t.Fatalf("unexpected public paths: got %#v want %#v", got["rails"].PublicPaths, want)
	}
	if got["web"].PublicPaths != nil {
		t.Fatalf("expected web PublicPaths nil when omitted, got %#v", got["web"].PublicPaths)
	}
}

func TestGatewayExposeRules_BareStringModeHasNoPublicPaths(t *testing.T) {
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
	if got := GatewayExposeRules(cfg)["rails"].PublicPaths; got != nil {
		t.Fatalf("expected bare-string rule PublicPaths nil, got %#v", got)
	}
}

func loadExposeConfig(t *testing.T, expose string) (*ProjectConfig, error) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), ".dev.toml")
	body := "[project]\nname = \"foocorp\"\n\n" + expose + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(cfgPath)
	return cfg, err
}

func TestGatewayExposeRules_ParsesPublicPaths(t *testing.T) {
	t.Parallel()

	cfg, err := loadExposeConfig(t, `
[gateway.expose.rails]
mode = "rewrite"
websocket_paths = ["/cable"]

[gateway.expose.rails.public_paths]
"/mcp" = "*"
"/oauth/token" = ["post"]
"/.well-known/oauth-protected-resource/" = ["GET"]
"/live" = ["websocket", "GET"]
`)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got := GatewayExposeRules(cfg)["rails"].PublicPaths
	want := PublicPaths{
		"/mcp":                                  {Any: true},
		"/oauth/token":                          {Methods: map[string]bool{"POST": true}},
		"/.well-known/oauth-protected-resource": {Methods: map[string]bool{"GET": true}},
		"/live":                                 {WebSocket: true, Methods: map[string]bool{"GET": true}},
		"/cable":                                {WebSocket: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("public paths mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestGatewayExposeRules_WebSocketPathsMergeIntoPublicPaths(t *testing.T) {
	t.Parallel()

	cfg, err := loadExposeConfig(t, `
[gateway.expose.rails]
websocket_paths = ["/cable"]

[gateway.expose.rails.public_paths]
"/cable" = ["GET"]
`)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	want := PublicPaths{"/cable": {WebSocket: true, Methods: map[string]bool{"GET": true}}}
	if got := GatewayExposeRules(cfg)["rails"].PublicPaths; !reflect.DeepEqual(got, want) {
		t.Fatalf("public paths mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestLoadProjectConfig_RejectsInvalidPublicPaths(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"relative path":  `"mcp" = "*"`,
		"empty list":     `"/mcp" = []`,
		"unknown method": `"/mcp" = ["FETCH"]`,
		"bad value":      `"/mcp" = true`,
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadExposeConfig(t, "[gateway.expose.rails.public_paths]\n"+entry)
			if err == nil || !strings.Contains(err.Error(), "gateway.expose.rails.public_paths") {
				t.Fatalf("expected public_paths error, got %v", err)
			}
		})
	}
}

func TestPublicPathsAllows(t *testing.T) {
	t.Parallel()

	paths := PublicPaths{
		"/":            {WebSocket: true},
		"/mcp":         {Any: true},
		"/oauth/token": {Methods: map[string]bool{"POST": true}},
		"/status":      {Methods: map[string]bool{"GET": true}},
	}
	cases := []struct {
		path      string
		method    string
		websocket bool
		want      bool
	}{
		{"/mcp", "POST", false, true},
		{"/mcp/sessions/1", "DELETE", false, true},
		{"/mcp", "GET", true, true},
		{"/mcpx", "POST", false, false},
		{"/oauth/token", "POST", false, true},
		{"/oauth/token", "GET", false, false},
		{"/status", "HEAD", false, true},
		{"/status", "GET", true, true},
		{"/dev_login", "GET", false, false},
		{"/dev_login", "GET", true, true},
		{"", "GET", false, false},
	}
	for _, tc := range cases {
		if got := paths.Allows(tc.path, tc.method, tc.websocket); got != tc.want {
			t.Errorf("Allows(%q, %q, websocket=%t) = %t, want %t", tc.path, tc.method, tc.websocket, got, tc.want)
		}
	}
}
