package router

import (
	"os"
	"path/filepath"
	"testing"

	"dev/internal/config"
)

func TestParseMatchers_SubdomainsAndGateway(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".dev.toml")
	body := `
[project]
name = "demo"

[gateway]
expose = { mailpit = { mode = "rewrite", debug_log = "tmp/gateway-http.log" } }

[process.mailpit]
singleton = true
command = "mailpit"
proxy = { subdomain = "mailpit" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	matchers := ParseMatchers(cfg)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Kind != SubdomainExplicit || matchers[0].Subdomain != "mailpit" {
		t.Fatalf("unexpected matcher: %+v", matchers[0])
	}
	if matchers[0].GatewayMode != config.GatewayModeRewrite {
		t.Fatalf("expected rewrite mode, got %q", matchers[0].GatewayMode)
	}
	if matchers[0].GatewayDebugLog != "tmp/gateway-http.log" {
		t.Fatalf("unexpected debug log: %q", matchers[0].GatewayDebugLog)
	}
	if !matchers[0].GatewayExposed {
		t.Fatalf("expected matcher to be gateway-exposed")
	}
}

func TestSelectMatcher_SubdomainPriority(t *testing.T) {
	matchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/deep/path", Match: "prefix"},
		{Process: "node", Subdomain: "app", Kind: SubdomainExplicit, Path: "/", Match: "prefix"},
	}
	best, ok := SelectMatcher(matchers, "app", "/deep/path/child")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestSelectMatcher_PathLengthAndPriority(t *testing.T) {
	pathMatchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix"},
		{Process: "node", Subdomain: "*", Kind: SubdomainWildcard, Path: "/api/v1", Match: "prefix"},
	}
	best, ok := SelectMatcher(pathMatchers, "app", "/api/v1/users")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}

	priorityMatchers := []Matcher{
		{Process: "rails", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix", Priority: 5},
		{Process: "node", Subdomain: "*", Kind: SubdomainWildcard, Path: "/", Match: "prefix", Priority: 10},
	}
	best, ok = SelectMatcher(priorityMatchers, "app", "/")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestMatchPath(t *testing.T) {
	ok, _ := MatchPath("/blah/x/y", "/blah/x", "prefix")
	if !ok {
		t.Fatalf("expected prefix match")
	}
	ok, _ = MatchPath("/rawz", "/raw*", "prefix")
	if !ok {
		t.Fatalf("expected raw prefix match")
	}
	ok, _ = MatchPath("/exact", "/exact", "exact")
	if !ok {
		t.Fatalf("expected exact match")
	}
	ok, _ = MatchPath("/exact/child", "/exact", "exact")
	if ok {
		t.Fatalf("expected exact mismatch")
	}
	ok, _ = MatchPath("/packs/app.js", "/packs/", "prefix")
	if !ok {
		t.Fatalf("expected trailing-slash prefix match")
	}
}
