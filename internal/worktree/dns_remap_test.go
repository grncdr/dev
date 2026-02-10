package worktree

import (
	"testing"

	"dev/internal/config"
)

func TestProxyDNSLabelForSlug(t *testing.T) {
	t.Parallel()

	cfg := &config.ProjectConfig{
		Project: config.ProjectBlock{
			MainSlug: "primary",
		},
		LocalDNS: config.ProjectLocalDNSBlock{
			Overrides: map[string]string{
				"main":         "foocorp",
				"feature/auth": "authz",
			},
		},
	}
	if got := ProxyDNSLabelForSlug(cfg, "primary"); got != "foocorp" {
		t.Fatalf("expected main remap for configured main slug, got %q", got)
	}
	if got := ProxyDNSLabelForSlug(cfg, "feature/auth"); got != "authz" {
		t.Fatalf("expected explicit remap, got %q", got)
	}
	if got := ProxyDNSLabelForSlug(cfg, "feature/payments"); got != "payments" {
		t.Fatalf("expected fallback dns label, got %q", got)
	}
}

func TestProxySlugForDNSLabel(t *testing.T) {
	t.Parallel()

	cfg := &config.ProjectConfig{
		Project: config.ProjectBlock{
			MainSlug: "primary",
		},
		LocalDNS: config.ProjectLocalDNSBlock{
			Overrides: map[string]string{
				"main":        "foocorp",
				"feature/api": "api",
			},
		},
	}
	if got, ok := ProxySlugForDNSLabel(cfg, "foocorp"); !ok || got != "primary" {
		t.Fatalf("expected mapped main slug primary, got %q ok=%v", got, ok)
	}
	if got, ok := ProxySlugForDNSLabel(cfg, "api"); !ok || got != "feature/api" {
		t.Fatalf("expected mapped feature slug, got %q ok=%v", got, ok)
	}
	if _, ok := ProxySlugForDNSLabel(cfg, "missing"); ok {
		t.Fatalf("expected no mapping for missing label")
	}
}
