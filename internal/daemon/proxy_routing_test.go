package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dev/internal/config"
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

func TestLocalProxyRouteForTunnelRequest_IncludesAuthCredentials(t *testing.T) {
	s := &Server{
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
		tunnels: map[string]*managedTunnel{
			"xyzz": {
				req: TunnelRequest{
					Slug:         "feature-branch",
					Label:        "xyzz",
					AuthUsername: "alice",
					AuthPassword: "secret",
				},
				status: "connected",
			},
		},
	}

	route, ok := s.localProxyRouteForTunnelRequest("xyzz.foocorp.dev")
	if !ok {
		t.Fatalf("expected route")
	}
	if route.LocalHost != "feature-branch.localhost" {
		t.Fatalf("expected rewritten host, got %q", route.LocalHost)
	}
	if route.RouteHost != "feature-branch.localhost" {
		t.Fatalf("expected canonical route host, got %q", route.RouteHost)
	}
	if route.AuthUsername != "alice" || route.AuthPassword != "secret" {
		t.Fatalf("expected auth credentials from tunnel request, got %+v", route)
	}
}

func TestLocalProxyHostForTunnelRequest_UsesDNSRemap(t *testing.T) {
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

[local-dns]
overrides = { main = "foocorp" }
`
	cfgPath := filepath.Join(repo, ".dev.toml")
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
					Slug:  "main",
					Label: "foocorp",
				},
				status: "connected",
			},
		},
	}
	got, ok := s.localProxyHostForTunnelRequest("app.foocorp.foocorp.dev")
	if !ok || got != "app.foocorp.localhost" {
		t.Fatalf("expected remapped host rewrite, got %q ok=%v", got, ok)
	}

	route, ok := s.localProxyRouteForTunnelRequest("app.foocorp.foocorp.dev")
	if !ok {
		t.Fatalf("expected route")
	}
	if route.LocalHost != "app.foocorp.localhost" {
		t.Fatalf("expected remapped local host, got %q", route.LocalHost)
	}
	if route.RouteHost != "app.main.localhost" {
		t.Fatalf("expected canonical route host to use tunnel slug, got %q", route.RouteHost)
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
			"priority":  int64(5),
		},
	}
	matchers := parseProxyMatchers("rails", raw, true, config.GatewayModeReverseProxy, "")
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
	if matchers[0].GatewayMode != config.GatewayModeReverseProxy {
		t.Fatalf("expected default gateway mode reverse_proxy, got %q", matchers[0].GatewayMode)
	}
	if matchers[1].GatewayMode != config.GatewayModeReverseProxy {
		t.Fatalf("expected default gateway mode reverse_proxy, got %q", matchers[1].GatewayMode)
	}
	if matchers[0].GatewayDebugLog != "" || matchers[1].GatewayDebugLog != "" {
		t.Fatalf("expected gateway debug log disabled by default")
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
	matchers := parseProxyMatchers("web", raw, false, config.GatewayModeReverseProxy, "")
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
	matchers := parseProxyMatchers("mailpit", raw, true, config.GatewayModeRewrite, "")
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
	matchers := parseProxyMatchers("postgres", raw, true, config.GatewayModeReverseProxy, "")
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].TCPListen != 15432 {
		t.Fatalf("expected tcp_listen 15432, got %d", matchers[0].TCPListen)
	}
}

func TestProcessProxyMatchers_FromLoadedInlineProxyConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

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
	matchers := processProxyMatchers(cfg)
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Process != "mailpit" || matchers[0].Subdomain != "mailpit" {
		t.Fatalf("unexpected matcher: %+v", matchers[0])
	}
	if matchers[0].GatewayMode != config.GatewayModeRewrite {
		t.Fatalf("expected rewrite gateway_mode, got %q", matchers[0].GatewayMode)
	}
	if matchers[0].GatewayDebugLog != "tmp/gateway-http.log" {
		t.Fatalf("expected gateway debug_log path, got %q", matchers[0].GatewayDebugLog)
	}
}

func TestProcessProxyMatchers_DefaultsToGatewayDisabledWhenNotExposed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[process.web]
command = "web"
proxy = { subdomain = "app" }
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
	if matchers[0].GatewayMode != config.GatewayModeDisable {
		t.Fatalf("expected disable gateway_mode, got %q", matchers[0].GatewayMode)
	}
}

func TestResolveProxyTargetForRequest_BlocksGatewayWhenProcessNotExposed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
	body := `
[project]
name = "foocorp"

[process.web]
proxy = { path = "/" }
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(dir)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "main",
		Project:  "foocorp",
		Path:     dir,
		DNSLabel: "main",
	})
	mgr.mu.Unlock()
	s := &Server{manager: mgr}
	_, _, _, _, _, _, _, err := s.resolveProxyTargetForRequest("main.localhost", "/", true)
	if !errors.Is(err, errGatewayProcessNotExposed) {
		t.Fatalf("expected gateway-not-exposed error, got %v", err)
	}
}

func TestProjectConfigForSlug_UsesManagerWorktreePath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".dev.toml")
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
	runtimeKey := runtimeKeyForPath(dir)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "monorepo",
		Project:  "foocorp",
		Path:     dir,
		DNSLabel: "monorepo",
	})
	mgr.mu.Unlock()
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

func TestResolveRequestedSlug_MapsDNSRemap(t *testing.T) {
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
main_slug = "primary"

[local-dns]
overrides = { main = "foocorp" }
`
	if err := os.WriteFile(filepath.Join(repo, ".dev.toml"), []byte(cfg), 0o600); err != nil {
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

	daemonCfg := &config.DaemonConfig{StateDir: filepath.Join(base, "state")}
	s := &Server{mainPath: repo, daemonConfig: daemonCfg}
	got, _, err := s.resolveRequestedSlug("foocorp")
	if err != nil {
		t.Fatalf("resolveRequestedSlug: %v", err)
	}
	if got != "primary" {
		t.Fatalf("expected primary, got %s", got)
	}
}

func TestResolveRequestedSlug_MapsRegisteredSlugDNSLabel(t *testing.T) {
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
`
	if err := os.WriteFile(filepath.Join(repo, ".dev.toml"), []byte(cfg), 0o600); err != nil {
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

	daemonCfg := &config.DaemonConfig{StateDir: filepath.Join(base, "state")}
	registerWorktreeForTest(t, daemonCfg, "foocorp", "feature/cloud-mailings", filepath.Join(base, "wt-cloud-mailings"), repo)
	s := &Server{mainPath: repo, daemonConfig: daemonCfg}
	got, _, err := s.resolveRequestedSlug("cloud-mailings")
	if err != nil {
		t.Fatalf("resolveRequestedSlug: %v", err)
	}
	if got != "feature/cloud-mailings" {
		t.Fatalf("expected feature/cloud-mailings, got %s", got)
	}
}

func TestResolveProxyTargetForRequest_AllowsDistinctMainOverridesAcrossProjects(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "repo-a")
	repoB := filepath.Join(base, "repo-b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoA, "init"); err != nil {
		t.Fatalf("git init repoA: %v", err)
	}
	if err := runGit(repoB, "init"); err != nil {
		t.Fatalf("git init repoB: %v", err)
	}

	cfgA := `
[project]
name = "org/repo-a"

[local-dns]
overrides = { main = "www" }

[process.web]
command = "echo a"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(repoA, ".dev.toml"), []byte(cfgA), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgB := `
[project]
name = "org/repo-b"

[local-dns]
overrides = { main = "blog" }

[process.web]
command = "echo b"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(repoB, ".dev.toml"), []byte(cfgB), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	keyA := runtimeKeyForPath(repoA)
	keyB := runtimeKeyForPath(repoB)
	m.mu.Lock()
	m.registerWorktreeLocked(keyA, runtimeWorktree{Slug: "main", Project: "org-repo-a", Path: repoA, DNSLabel: "www"})
	m.registerWorktreeLocked(keyB, runtimeWorktree{Slug: "main", Project: "org-repo-b", Path: repoB, DNSLabel: "blog"})
	m.processes[keyA] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	m.processes[keyB] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	m.mu.Unlock()

	s := &Server{
		manager:      m,
		mainPath:     repoA,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
	}
	_, _, process, _, _, targetSlug, _, err := s.resolveProxyTargetForRequest("www.localhost", "/", false)
	if err != nil {
		t.Fatalf("resolveProxyTargetForRequest: %v", err)
	}
	if targetSlug != "main" {
		t.Fatalf("expected target slug main, got %q", targetSlug)
	}
	if process != "web" {
		t.Fatalf("expected process web, got %q", process)
	}
}

func TestResolveProxyTargetForRequest_AllowsDottedMainOverride(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init repo: %v", err)
	}

	cfg := `
[project]
name = "org/repo"
main_slug = "primary"

[local-dns]
overrides = { main = "www.foocorp" }

[process.web]
command = "echo ok"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(repo, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	daemonCfg := &config.DaemonConfig{StateDir: filepath.Join(base, "state"), LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}}
	registerWorktreeForTest(t, daemonCfg, "org-repo", "primary", repo, repo)

	mgr := NewManager()
	runtimeKey := runtimeKeyForPath(repo)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(runtimeKey, runtimeWorktree{
		Slug:     "primary",
		Project:  "org-repo",
		Path:     repo,
		DNSLabel: "www.foocorp",
	})
	mgr.processes[runtimeKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager:      mgr,
		mainPath:     repo,
		daemonConfig: daemonCfg,
	}
	_, _, process, _, _, targetSlug, _, err := s.resolveProxyTargetForRequest("www.foocorp.localhost", "/", false)
	if err != nil {
		t.Fatalf("resolveProxyTargetForRequest: %v", err)
	}
	if targetSlug != "primary" {
		t.Fatalf("expected target slug primary, got %q", targetSlug)
	}
	if process != "web" {
		t.Fatalf("expected process web, got %q", process)
	}
}

func TestResolveProxyTargetForRequest_PrefersSubdomainRouteOverDottedCandidateFallback(t *testing.T) {
	base := t.TempDir()
	websiteRepo := filepath.Join(base, "website")
	appRepo := filepath.Join(base, "app")
	if err := os.MkdirAll(websiteRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(websiteRepo, "init"); err != nil {
		t.Fatalf("git init website: %v", err)
	}
	if err := runGit(appRepo, "init"); err != nil {
		t.Fatalf("git init app: %v", err)
	}

	websiteCfg := `
[project]
name = "org/website"

[local-dns]
overrides = { main = "www.foocorp" }

[process.web]
command = "echo website"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(websiteRepo, ".dev.toml"), []byte(websiteCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	appCfg := `
[project]
name = "org/app"

[process.web]
command = "echo app"
proxy = { subdomain = "app", path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(appRepo, ".dev.toml"), []byte(appCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	loadedWebsiteCfg, _, err := config.LoadProjectConfig(filepath.Join(websiteRepo, ".dev.toml"))
	if err != nil {
		t.Fatalf("load website config: %v", err)
	}

	mgr := NewManager()
	websiteKey := runtimeKeyForPath(websiteRepo)
	appKey := runtimeKeyForPath(appRepo)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(websiteKey, runtimeWorktree{
		Slug:     "main",
		Project:  "org-website",
		Path:     websiteRepo,
		DNSLabel: "www.foocorp",
	})
	mgr.registerWorktreeLocked(appKey, runtimeWorktree{
		Slug:     "foocorp",
		Project:  "org-app",
		Path:     appRepo,
		DNSLabel: "foocorp",
	})
	mgr.processes[websiteKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.processes[appKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager:      mgr,
		mainPath:     websiteRepo,
		config:       loadedWebsiteCfg,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
	}

	_, _, process, _, _, targetSlug, targetPath, err := s.resolveProxyTargetForRequest("app.foocorp.localhost", "/", false)
	if err != nil {
		t.Fatalf("resolveProxyTargetForRequest: %v", err)
	}
	if process != "web" {
		t.Fatalf("expected process web, got %q", process)
	}
	if targetSlug != "foocorp" {
		t.Fatalf("expected target slug foocorp, got %q", targetSlug)
	}
	if targetPath != appRepo {
		t.Fatalf("expected target path %q, got %q", appRepo, targetPath)
	}
}

func TestResolveProxyTargetForRequest_MultiProjectFoocorpSubdomainsRouteToAppProject(t *testing.T) {
	base := t.TempDir()
	websiteRepo := filepath.Join(base, "website")
	appRepo := filepath.Join(base, "app")
	if err := os.MkdirAll(websiteRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(websiteRepo, "init"); err != nil {
		t.Fatalf("git init website: %v", err)
	}
	if err := runGit(appRepo, "init"); err != nil {
		t.Fatalf("git init app: %v", err)
	}

	websiteCfg := `
[project]
name = "org/website"

[local-dns]
overrides = { main = "www.foocorp" }

[process.web]
command = "echo website"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(websiteRepo, ".dev.toml"), []byte(websiteCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	appCfg := `
[project]
name = "org/app"
main_slug = "foocorp"

[local-dns]
overrides = { main = "foocorp" }

[process.root]
command = "echo app-root"
proxy = { path = "/" }
port = "unix"

[process.frontend]
command = "echo app-frontend"
proxy = { subdomains = ["app", "login"], path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(appRepo, ".dev.toml"), []byte(appCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	loadedWebsiteCfg, _, err := config.LoadProjectConfig(filepath.Join(websiteRepo, ".dev.toml"))
	if err != nil {
		t.Fatalf("load website config: %v", err)
	}

	mgr := NewManager()
	websiteKey := runtimeKeyForPath(websiteRepo)
	appKey := runtimeKeyForPath(appRepo)
	mgr.mu.Lock()
	mgr.registerWorktreeLocked(websiteKey, runtimeWorktree{
		Slug:     "main",
		Project:  "org-website",
		Path:     websiteRepo,
		DNSLabel: "www.foocorp",
	})
	mgr.registerWorktreeLocked(appKey, runtimeWorktree{
		Slug:     "foocorp",
		Project:  "org-app",
		Path:     appRepo,
		DNSLabel: "foocorp",
	})
	mgr.processes[websiteKey] = map[string]*processInfo{
		"web": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.processes[appKey] = map[string]*processInfo{
		"root": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
		"frontend": {
			network: "tcp",
			address: "127.0.0.1:1",
			cmd:     &exec.Cmd{Process: &os.Process{Pid: 1}},
			ready:   true,
			exited:  make(chan struct{}),
		},
	}
	mgr.mu.Unlock()

	s := &Server{
		manager:      mgr,
		mainPath:     websiteRepo,
		config:       loadedWebsiteCfg,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
	}

	assertRoute := func(host, wantProcess, wantSlug, wantPath string) {
		t.Helper()
		_, _, process, _, _, targetSlug, targetPath, err := s.resolveProxyTargetForRequest(host, "/", false)
		if err != nil {
			t.Fatalf("resolveProxyTargetForRequest(%q): %v", host, err)
		}
		if process != wantProcess {
			t.Fatalf("%s expected process %q, got %q", host, wantProcess, process)
		}
		if targetSlug != wantSlug {
			t.Fatalf("%s expected target slug %q, got %q", host, wantSlug, targetSlug)
		}
		if targetPath != wantPath {
			t.Fatalf("%s expected target path %q, got %q", host, wantPath, targetPath)
		}
	}

	assertRoute("app.foocorp.localhost", "frontend", "foocorp", appRepo)
	assertRoute("login.foocorp.localhost", "frontend", "foocorp", appRepo)
	assertRoute("foocorp.localhost", "root", "foocorp", appRepo)
	assertRoute("www.foocorp.localhost", "web", "main", websiteRepo)
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
