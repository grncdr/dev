package agent

import (
	"strings"
	"testing"
)

func TestMTLSClientForGatewayURL_MissingCredentials(t *testing.T) {
	_, _, err := MTLSClientForGatewayURL("https://gw.example.test", "")
	if err == nil {
		t.Fatalf("expected missing credential error")
	}
	if !strings.Contains(err.Error(), "gateway login") {
		t.Fatalf("expected login hint, got: %v", err)
	}
}
