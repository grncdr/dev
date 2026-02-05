package cli

import (
	"testing"

	"dev-mode/internal/config"
	"dev-mode/internal/daemon"
)

func TestGatewayProxyURLsByProcess_OnlyExposedProcesses(t *testing.T) {
	cfg := &config.ProjectConfig{
		Gateway: map[string]any{
			"url":    "https://gw.foocorp.dev",
			"expose": map[string]any{"rails": map[string]any{"mode": "rewrite"}},
		},
	}
	localByProcess := map[string][]string{
		"rails":   {"https://main.localhost/", "https://app.main.localhost/admin"},
		"webpack": {"https://assets.main.localhost/"},
	}
	tunnel := &daemon.TunnelStatus{
		Label:      "xyzz",
		PublicHost: "wip.foocorp.dev",
		Status:     "connected",
	}

	got := gatewayProxyURLsByProcess(localByProcess, cfg, tunnel)
	if len(got["rails"]) != 2 {
		t.Fatalf("expected rails routes, got %+v", got["rails"])
	}
	if _, ok := got["webpack"]; ok {
		t.Fatalf("expected webpack to be excluded, got %+v", got["webpack"])
	}
}
