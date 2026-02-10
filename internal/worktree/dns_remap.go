package worktree

import (
	"fmt"
	"sort"
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

// ResolveSlugForDNSLabel resolves a proxy DNS label back to a registered worktree
// slug for the current project.
//
// Resolution order:
// 1. Explicit local-dns override mapping.
// 2. Registered worktrees whose effective DNS label (including remaps) matches.
func ResolveSlugForDNSLabel(cfg *config.ProjectConfig, daemonCfg *config.DaemonConfig, dnsLabel string) (string, bool, error) {
	if mapped, ok := ProxySlugForDNSLabel(cfg, dnsLabel); ok {
		return mapped, true, nil
	}
	if cfg == nil {
		return "", false, nil
	}
	label, err := normalizeDNSRemapValue(dnsLabel)
	if err != nil {
		return "", false, nil
	}
	project, err := NormalizeIdentifierSegment(cfg.Project.Name)
	if err != nil {
		return "", false, nil
	}
	registered, err := ListRegisteredWorktrees(daemonCfg, project)
	if err != nil {
		return "", false, err
	}
	matches := []string{}
	for _, entry := range registered {
		if entry.Slug == "" {
			continue
		}
		if ProxyDNSLabelForSlug(cfg, entry.Slug) == label {
			matches = append(matches, entry.Slug)
		}
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	sort.Strings(matches)
	matches = dedupeStringValues(matches)
	if len(matches) > 1 {
		return "", false, fmt.Errorf("dns label %q maps to multiple worktree slugs (%s); configure local-dns.overrides in .dev.toml or .dev.local.toml", dnsLabel, strings.Join(matches, ", "))
	}
	return matches[0], true, nil
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

func dedupeStringValues(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	last := ""
	for i, value := range values {
		if i == 0 || value != last {
			out = append(out, value)
		}
		last = value
	}
	return out
}
