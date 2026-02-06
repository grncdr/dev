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

type GatewayExposeRule struct {
	Mode     string
	DebugLog string
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
	modeRaw, ok := m["mode"]
	if !ok || modeRaw == nil {
		return GatewayExposeRule{
			Mode:     GatewayModeReverseProxy,
			DebugLog: debugLog,
		}
	}
	mode, ok := normalizeGatewayModeValue(modeRaw)
	if !ok {
		return GatewayExposeRule{Mode: GatewayModeDisable}
	}
	return GatewayExposeRule{
		Mode:     mode,
		DebugLog: debugLog,
	}
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
