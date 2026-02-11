package daemon

import (
	"reflect"
	"testing"

	"dev/internal/router"
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
