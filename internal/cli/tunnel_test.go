package cli

import "testing"

func TestGatewayPublicURL(t *testing.T) {
	got := gatewayPublicURL("https://gateway.foocorp.dev", "tunnels.example.test", "alpha")
	if got != "https://alpha.tunnels.example.test/" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestGatewayPublicURLRequiresPublicHost(t *testing.T) {
	got := gatewayPublicURL("https://gateway.foocorp.dev", "", "alpha")
	if got != "" {
		t.Fatalf("expected empty URL, got %s", got)
	}
}
