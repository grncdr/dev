package cli

import "testing"

func TestGatewayPublicURL(t *testing.T) {
	got := gatewayPublicURL("https://tunnels.example.test", "alpha")
	if got != "https://alpha.tunnels.example.test/" {
		t.Fatalf("unexpected URL: %s", got)
	}
}
