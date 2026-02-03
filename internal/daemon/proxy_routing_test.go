package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"dev-mode/internal/config"
)

func TestParseProxyHost(t *testing.T) {
	s := &Server{daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}}}
	slug, subdomain, err := s.parseProxyHost("foo.localhost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != "foo" || subdomain != "" {
		t.Fatalf("expected slug foo, empty subdomain; got %q %q", slug, subdomain)
	}
	slug, subdomain, err = s.parseProxyHost("app.foo.localhost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != "foo" || subdomain != "app" {
		t.Fatalf("expected slug foo, subdomain app; got %q %q", slug, subdomain)
	}
	_, _, err = s.parseProxyHost("foo.otherhost")
	if err == nil {
		t.Fatalf("expected apex mismatch error")
	}
}

func TestParseProxyHost_UsesDaemonApexZone(t *testing.T) {
	s := &Server{daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".example.test"}}}
	slug, subdomain, err := s.parseProxyHost("app.foo.example.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slug != "foo" || subdomain != "app" {
		t.Fatalf("expected foo/app got %q/%q", slug, subdomain)
	}
}

func TestLocalProxyHostForTunnelRequest(t *testing.T) {
	s := &Server{
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
		tunnels: map[string]*managedTunnel{
			"xyzz": {
				req: TunnelRequest{
					Slug:  "feature-branch",
					Label: "xyzz",
				},
				status: "connected",
			},
			"app": {
				req: TunnelRequest{
					Slug:  "app-slug",
					Label: "app",
				},
				status: "connected",
			},
		},
	}

	got, ok := s.localProxyHostForTunnelRequest("xyzz.foocorp.dev")
	if !ok || got != "feature-branch.localhost" {
		t.Fatalf("expected base tunnel host rewrite, got %q ok=%v", got, ok)
	}

	got, ok = s.localProxyHostForTunnelRequest("app.xyzz.foocorp.dev")
	if !ok || got != "app.feature-branch.localhost" {
		t.Fatalf("expected subdomain tunnel host rewrite, got %q ok=%v", got, ok)
	}

	_, ok = s.localProxyHostForTunnelRequest("unknown.foocorp.dev")
	if ok {
		t.Fatalf("expected no rewrite for unknown label")
	}
}

func TestLocalProxyHostForTunnelRequest_UsesMainSlugOverride(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "monorepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfgBody := `
[project]
name = "foocorp"
main_slug = "foocorp"
`
	cfgPath := filepath.Join(repo, ".dev-mode.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repo, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	s := &Server{
		mainPath:     repo,
		config:       cfg,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
		tunnels: map[string]*managedTunnel{
			"foocorp": {
				req: TunnelRequest{
					Slug:  "monorepo",
					Label: "foocorp",
				},
				status: "connected",
			},
		},
	}
	got, ok := s.localProxyHostForTunnelRequest("app.foocorp.foocorp.dev")
	if !ok || got != "app.foocorp.localhost" {
		t.Fatalf("expected main_slug host rewrite, got %q ok=%v", got, ok)
	}
}

func TestParseProxyMatchers(t *testing.T) {
	raw := []any{
		map[string]any{
			"subdomain": nil,
			"path":      "/",
			"match":     "prefix",
		},
		map[string]any{
			"subdomain": "*",
			"path":      "/api",
			"match":     "prefix",
			"mode":      "transparent",
			"priority":  int64(5),
		},
	}
	matchers := parseProxyMatchers("rails", raw, true)
	if len(matchers) != 2 {
		t.Fatalf("expected 2 matchers, got %d", len(matchers))
	}
	if matchers[0].Kind != subdomainBase || matchers[0].Subdomain != "" {
		t.Fatalf("expected base matcher for first entry")
	}
	if matchers[1].Kind != subdomainWildcard {
		t.Fatalf("expected wildcard matcher for second entry")
	}
	if matchers[1].Priority != 5 {
		t.Fatalf("expected priority 5, got %d", matchers[1].Priority)
	}
	if matchers[0].Mode != "reverse" {
		t.Fatalf("expected default mode reverse, got %q", matchers[0].Mode)
	}
	if matchers[1].Mode != "transparent" {
		t.Fatalf("expected transparent mode, got %q", matchers[1].Mode)
	}
	if !matchers[0].Singleton || !matchers[1].Singleton {
		t.Fatalf("expected singleton to propagate to matchers")
	}
}

func TestParseProxyMatchers_SubdomainsList(t *testing.T) {
	raw := map[string]any{
		"subdomains": []any{"app", "api", "*"},
		"path":       "/",
	}
	matchers := parseProxyMatchers("web", raw, false)
	if len(matchers) != 3 {
		t.Fatalf("expected 3 matchers, got %d", len(matchers))
	}
	kinds := map[subdomainMatchKind]int{}
	for _, m := range matchers {
		kinds[m.Kind]++
	}
	if kinds[subdomainExplicit] != 2 || kinds[subdomainWildcard] != 1 {
		t.Fatalf("unexpected kinds: %+v", kinds)
	}
}

func TestParseProxyMatchers_InlineStringMap(t *testing.T) {
	raw := map[string]string{
		"subdomain": "mailpit",
	}
	matchers := parseProxyMatchers("mailpit", raw, true)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Kind != subdomainExplicit || matchers[0].Subdomain != "mailpit" {
		t.Fatalf("unexpected matcher: %+v", matchers[0])
	}
	if matchers[0].Path != "/" || matchers[0].Match != "prefix" {
		t.Fatalf("expected default path/match, got %+v", matchers[0])
	}
}

func TestParseProxyMatchers_TCPListen(t *testing.T) {
	raw := map[string]any{
		"tcp_listen": int64(15432),
	}
	matchers := parseProxyMatchers("postgres", raw, true)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].TCPListen != 15432 {
		t.Fatalf("expected tcp_listen 15432, got %d", matchers[0].TCPListen)
	}
}

func TestProcessProxyMatchers_FromLoadedInlineProxyConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
	body := `
[project]
name = "foocorp"

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
	matchers := processProxyMatchers(cfg)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Process != "mailpit" || matchers[0].Subdomain != "mailpit" {
		t.Fatalf("unexpected matcher: %+v", matchers[0])
	}
}

func TestProjectConfigForSlug_UsesManagerWorktreePath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev-mode.toml")
	body := `
[project]
name = "foocorp"

[process.mailpit]
command = "mailpit"
proxy = { subdomain = "mailpit" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.paths["monorepo"] = dir
	s := &Server{manager: mgr}

	cfg, repoPath, err := s.projectConfigForSlug("monorepo")
	if err != nil {
		t.Fatalf("projectConfigForSlug: %v", err)
	}
	if repoPath != dir {
		t.Fatalf("expected repo path %s, got %s", dir, repoPath)
	}
	if _, ok := cfg.Processes["mailpit"]; !ok {
		t.Fatalf("expected mailpit process in loaded config")
	}
}

func TestSelectProxyMatcher_SubdomainPriority(t *testing.T) {
	matchers := []proxyMatcher{
		{Process: "rails", Subdomain: "*", Kind: subdomainWildcard, Path: "/deep/path", Match: "prefix"},
		{Process: "node", Subdomain: "app", Kind: subdomainExplicit, Path: "/", Match: "prefix"},
	}
	best, ok := selectProxyMatcher(matchers, "app", "/deep/path/child")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestResolveRequestedSlug_MapsMainSlugOverride(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "monorepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfg := `
[project]
name = "foocorp"
main_slug = "foocorp"
`
	if err := os.WriteFile(filepath.Join(repo, ".dev-mode.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repo, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	s := &Server{mainPath: repo}
	got, err := s.resolveRequestedSlug("foocorp")
	if err != nil {
		t.Fatalf("resolveRequestedSlug: %v", err)
	}
	if got != "monorepo" {
		t.Fatalf("expected monorepo, got %s", got)
	}
}

func TestSelectProxyMatcher_PathLength(t *testing.T) {
	matchers := []proxyMatcher{
		{Process: "rails", Subdomain: "*", Kind: subdomainWildcard, Path: "/", Match: "prefix"},
		{Process: "node", Subdomain: "*", Kind: subdomainWildcard, Path: "/api/v1", Match: "prefix"},
	}
	best, ok := selectProxyMatcher(matchers, "app", "/api/v1/users")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestSelectProxyMatcher_Priority(t *testing.T) {
	matchers := []proxyMatcher{
		{Process: "rails", Subdomain: "*", Kind: subdomainWildcard, Path: "/", Match: "prefix", Priority: 5},
		{Process: "node", Subdomain: "*", Kind: subdomainWildcard, Path: "/", Match: "prefix", Priority: 10},
	}
	best, ok := selectProxyMatcher(matchers, "app", "/")
	if !ok {
		t.Fatalf("expected matcher")
	}
	if best.Process != "node" {
		t.Fatalf("expected node, got %s", best.Process)
	}
}

func TestMatchPath(t *testing.T) {
	ok, _ := matchPath("/blah/x/y", "/blah/x", "prefix")
	if !ok {
		t.Fatalf("expected prefix match")
	}
	ok, _ = matchPath("/rawz", "/raw*", "prefix")
	if !ok {
		t.Fatalf("expected raw prefix match")
	}
	ok, _ = matchPath("/exact", "/exact", "exact")
	if !ok {
		t.Fatalf("expected exact match")
	}
	ok, _ = matchPath("/exact/child", "/exact", "exact")
	if ok {
		t.Fatalf("expected exact mismatch")
	}
	ok, _ = matchPath("/packs/app.js", "/packs/", "prefix")
	if !ok {
		t.Fatalf("expected trailing-slash prefix match")
	}
}
