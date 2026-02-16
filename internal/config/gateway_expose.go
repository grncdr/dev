package config

import (
	"fmt"
	"reflect"
	"strings"
)

const (
	GatewayModeDisable      = "disable"
	GatewayModeReverseProxy = "reverse_proxy"
	GatewayModeRewrite      = "rewrite"
)

// GatewayExposeRule is a parsed entry from the [gateway.expose] table,
// describing how a process is exposed through the gateway tunnel.
type GatewayExposeRule struct {
	// Mode is the proxy strategy: "reverse_proxy", "rewrite", or "disable".
	Mode string
	// DebugLog is an optional relative path for HTTP transcript logging.
	DebugLog string
	// RewritePeerSubdomains controls rewrite-mode translation of peer local
	// hostnames. Values are normalized to lowercase and trimmed. "*" rewrites
	// all peer subdomains.
	RewritePeerSubdomains []string
}

// GatewayExposeModes returns gateway-exposed process modes keyed by process name.
// Only processes listed in gateway.expose are returned.
func GatewayExposeModes(cfg *ProjectConfig) map[string]string {
	rules := GatewayExposeRules(cfg)
	out := map[string]string{}
	for name, rule := range rules {
		out[name] = rule.Mode
	}
	return out
}

// GatewayExposeRules returns parsed gateway.expose entries keyed by process name.
// Only processes listed in gateway.expose are returned.
func GatewayExposeRules(cfg *ProjectConfig) map[string]GatewayExposeRule {
	out := map[string]GatewayExposeRule{}
	if cfg == nil || cfg.Gateway == nil {
		return out
	}
	rawExpose, ok := cfg.Gateway["expose"]
	if !ok || rawExpose == nil {
		return out
	}
	exposeMap, ok := normalizeStringMap(rawExpose)
	if !ok {
		return out
	}
	for rawName, rawRule := range exposeMap {
		name := strings.TrimSpace(rawName)
		if name == "" {
			continue
		}
		rule := parseGatewayExposeRule(rawRule)
		if rule.Mode == GatewayModeDisable {
			continue
		}
		out[name] = rule
	}
	return out
}

func parseGatewayExposeRule(raw any) GatewayExposeRule {
	if raw == nil {
		return GatewayExposeRule{Mode: GatewayModeReverseProxy}
	}
	if mode, ok := normalizeGatewayModeValue(raw); ok {
		return GatewayExposeRule{Mode: mode}
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return GatewayExposeRule{Mode: GatewayModeDisable}
	}
	debugLog := ""
	if val, ok := m["debug_log"].(string); ok {
		debugLog = strings.TrimSpace(val)
	}
	rewritePeerSubdomains := normalizeGatewayRewritePeerSubdomains(m["rewrite_peer_subdomains"])
	modeRaw, ok := m["mode"]
	if !ok || modeRaw == nil {
		return GatewayExposeRule{
			Mode:                  GatewayModeReverseProxy,
			DebugLog:              debugLog,
			RewritePeerSubdomains: rewritePeerSubdomains,
		}
	}
	mode, ok := normalizeGatewayModeValue(modeRaw)
	if !ok {
		return GatewayExposeRule{Mode: GatewayModeDisable}
	}
	return GatewayExposeRule{
		Mode:                  mode,
		DebugLog:              debugLog,
		RewritePeerSubdomains: rewritePeerSubdomains,
	}
}

func normalizeGatewayRewritePeerSubdomains(raw any) []string {
	if raw == nil {
		return nil
	}
	val := reflect.ValueOf(raw)
	if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, val.Len())
	for i := 0; i < val.Len(); i++ {
		value := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", val.Index(i).Interface())))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeGatewayModeValue(raw any) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", raw)))
	switch mode {
	case GatewayModeReverseProxy:
		return GatewayModeReverseProxy, true
	case GatewayModeRewrite:
		return GatewayModeRewrite, true
	case GatewayModeDisable:
		return GatewayModeDisable, true
	default:
		return "", false
	}
}

func normalizeStringMap(raw any) (map[string]any, bool) {
	if raw == nil {
		return nil, false
	}
	val := reflect.ValueOf(raw)
	if val.Kind() != reflect.Map {
		return nil, false
	}
	out := map[string]any{}
	for _, key := range val.MapKeys() {
		if key.Kind() != reflect.String {
			continue
		}
		out[key.String()] = val.MapIndex(key).Interface()
	}
	return out, true
}
