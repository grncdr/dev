package cli

import (
	"testing"

	"dev/internal/config"
)

func TestGatewayPublicURL(t *testing.T) {
	t.Parallel()

	got := gatewayPublicURL("https://gateway.foocorp.dev", "tunnels.example.test", "alpha")
	if got != "https://alpha.tunnels.example.test/" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestGatewayPublicURLRequiresPublicHost(t *testing.T) {
	t.Parallel()

	got := gatewayPublicURL("https://gateway.foocorp.dev", "", "alpha")
	if got != "" {
		t.Fatalf("expected empty URL, got %s", got)
	}
}

func TestParseShareAuthArg(t *testing.T) {
	t.Parallel()

	user, pass, err := parseShareAuthArg("alice:secret")
	if err != nil {
		t.Fatalf("parseShareAuthArg: %v", err)
	}
	if user != "alice" || pass != "secret" {
		t.Fatalf("unexpected parsed auth: %q/%q", user, pass)
	}
}

func TestResolveShareAuth_PrefersCommandLine(t *testing.T) {
	t.Parallel()

	cfg := &config.ProjectConfig{
		Gateway: map[string]any{
			"auth": map[string]any{
				"username": "default-user",
				"password": "default-pass",
			},
		},
	}
	user, pass, err := resolveShareAuth(cfg, "cli-user:cli-pass", false)
	if err != nil {
		t.Fatalf("resolveShareAuth: %v", err)
	}
	if user != "cli-user" || pass != "cli-pass" {
		t.Fatalf("expected command auth override, got %q/%q", user, pass)
	}
}

func TestResolveShareAuth_NoAuthDisablesDefaults(t *testing.T) {
	t.Parallel()

	cfg := &config.ProjectConfig{
		Gateway: map[string]any{
			"auth": map[string]any{
				"username": "default-user",
				"password": "default-pass",
			},
		},
	}
	user, pass, err := resolveShareAuth(cfg, "", true)
	if err != nil {
		t.Fatalf("resolveShareAuth: %v", err)
	}
	if user != "" || pass != "" {
		t.Fatalf("expected no auth credentials, got %q/%q", user, pass)
	}
}

func TestResolveShareAuth_RejectsConflictingFlags(t *testing.T) {
	t.Parallel()

	_, _, err := resolveShareAuth(nil, "alice:secret", true)
	if err == nil {
		t.Fatalf("expected conflict error")
	}
}
