package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dev/internal/config"
	"dev/internal/router"
	"dev/internal/worktree"
)

func TestLocalProxyRoutesByProcess(t *testing.T) {
	matchers := []router.Matcher{
		{Process: "rails", Kind: router.SubdomainBase, Path: "/"},
		{Process: "rails", Kind: router.SubdomainExplicit, Subdomain: "app", Path: "/admin"},
		{Process: "db", TCPListen: 15432},
	}
	got := localProxyRoutesByProcess(matchers, "main", ".localhost")
	want := map[string][]string{
		"rails": {"https://app.main.localhost/admin", "https://main.localhost/"},
		"db":    {"tcp://127.0.0.1:15432"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("localProxyRoutesByProcess mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestGatewayRoutesByProcess_OnlyExposedProcesses(t *testing.T) {
	localByProcess := map[string][]string{
		"rails":   {"https://main.localhost/", "https://app.main.localhost/admin"},
		"webpack": {"https://assets.main.localhost/"},
	}
	exposed := map[string]bool{"rails": true}
	tunnel := &TunnelStatus{
		Label:      "xyzz",
		PublicHost: "wip.foocorp.dev",
		Status:     "connected",
	}
	got := gatewayRoutesByProcess(localByProcess, exposed, tunnel)
	want := map[string][]string{
		"rails": {"https://app.xyzz.wip.foocorp.dev/admin", "https://xyzz.wip.foocorp.dev/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gatewayRoutesByProcess mismatch:\nwant: %#v\ngot:  %#v", want, got)
	}
}

// `dev status` run inside a worktree sends no project qualifier. The tunnel
// must still be found through the project named in the worktree's config.
func TestRoutingStatusForWorktree_FindsTunnelWithoutProjectQualifier(t *testing.T) {
	repo := t.TempDir()
	if err := runGit(repo, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfg := `
[project]
name = "org/app"

[gateway]
url = "https://gateway.example.test"

[gateway.expose]
web = { mode = "rewrite" }

[process.web]
command = "echo web"
proxy = { path = "/" }
port = "unix"
`
	if err := os.WriteFile(filepath.Join(repo, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	slug := defaultSlugForRepo(t, repo)

	s := &Server{
		manager:      NewManager(),
		mainPath:     repo,
		daemonConfig: &config.DaemonConfig{LocalProxy: config.DaemonLocalProxyBlock{ApexZone: ".localhost"}},
		agents: seededAgentsForTunnels(map[string]*managedTunnel{
			"app": {
				req: TunnelRequest{
					Identifier: worktree.Identifier{Project: "org/app", Slug: slug},
					Label:      "app",
					GatewayURL: "https://gateway.example.test",
				},
				publicHost: "foocorp.dev",
				status:     "connected",
			},
		}),
	}

	routing := s.routingStatusForWorktree(slug, "", repo)
	if routing.GatewayStatus != "connected" {
		t.Fatalf("expected gateway status connected, got %q", routing.GatewayStatus)
	}
	if len(routing.Gateway["web"]) == 0 {
		t.Fatalf("expected gateway routes for web, got %#v", routing.Gateway)
	}
}
