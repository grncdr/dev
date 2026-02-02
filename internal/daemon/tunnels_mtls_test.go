package daemon

import (
	"strings"
	"testing"

	"dev-mode/internal/config"
)

func TestGatewayMTLSClientMissingCredentials(t *testing.T) {
	userCfg := &config.UserConfig{}
	_, _, err := gatewayMTLSClient("https://gw.example.test", userCfg)
	if err == nil {
		t.Fatalf("expected missing credential error")
	}
	if !strings.Contains(err.Error(), "gateway login") {
		t.Fatalf("expected login hint, got: %v", err)
	}
}
