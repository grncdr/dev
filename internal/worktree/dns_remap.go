package worktree

import (
	"strings"

	"dev/internal/config"
)

func ProxyDNSLabelForSlug(cfg *config.ProjectConfig, slug string) string {
	fallback := SlugDNSLabel(strings.TrimSpace(strings.ToLower(slug)))
	if cfg == nil {
		return fallback
	}
	normalizedSlug, err := NormalizeIdentifierSegment(slug)
	if err != nil {
		return fallback
	}
	remapSlug := normalizedSlug
	if remapped, ok := remappedDNSLabelForSlug(cfg, remapSlug); ok {
		return remapped
	}
	if configuredMain, ok := configuredMainSlug(cfg); ok && normalizedSlug == configuredMain {
		if remapped, ok := remappedDNSLabelForSlug(cfg, "main"); ok {
			return remapped
		}
	}
	return SlugDNSLabel(normalizedSlug)
}

func ProxySlugForDNSLabel(cfg *config.ProjectConfig, dnsLabel string) (string, bool) {
	if cfg == nil || len(cfg.LocalDNS.Overrides) == 0 {
		return "", false
	}
	label, err := normalizeDNSRemapValue(dnsLabel)
	if err != nil {
		return "", false
	}
	for rawSlug, rawLabel := range cfg.LocalDNS.Overrides {
		slug, err := NormalizeIdentifierSegment(rawSlug)
		if err != nil {
			continue
		}
		normalizedLabel, err := normalizeDNSRemapValue(rawLabel)
		if err != nil {
			continue
		}
		if normalizedLabel != label {
			continue
		}
		if slug == "main" {
			if configuredMain, ok := configuredMainSlug(cfg); ok {
				return configuredMain, true
			}
		}
		return slug, true
	}
	return "", false
}

func remappedDNSLabelForSlug(cfg *config.ProjectConfig, slug string) (string, bool) {
	for rawSlug, rawLabel := range cfg.LocalDNS.Overrides {
		normalizedSlug, err := NormalizeIdentifierSegment(rawSlug)
		if err != nil || normalizedSlug != slug {
			continue
		}
		label, err := normalizeDNSRemapValue(rawLabel)
		if err != nil {
			return "", false
		}
		return label, true
	}
	return "", false
}

func normalizeDNSRemapValue(value string) (string, error) {
	normalized, err := NormalizeIdentifierSegment(value)
	if err != nil {
		return "", err
	}
	return SlugDNSLabel(normalized), nil
}

func configuredMainSlug(cfg *config.ProjectConfig) (string, bool) {
	if cfg == nil {
		return "", false
	}
	normalized, err := NormalizeIdentifierSegment(cfg.Project.MainSlug)
	if err != nil || normalized == "" {
		return "", false
	}
	return normalized, true
}
