package daemon

import (
	"strings"
	"testing"

	"dev/internal/config"
	"dev/internal/gateway"
)

func TestGatewayMTLSClientMissingCredentials(t *testing.T) {
	daemonCfg := &config.DaemonConfig{}
	_, _, err := gateway.MTLSClientForGatewayURL("https://gw.example.test", daemonCfg)
	if err == nil {
		t.Fatalf("expected missing credential error")
	}
	if !strings.Contains(err.Error(), "gateway login") {
		t.Fatalf("expected login hint, got: %v", err)
	}
}
