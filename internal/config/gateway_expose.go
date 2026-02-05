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

// GatewayExposeModes returns gateway-exposed process modes keyed by process name.
// Only processes listed in gateway.expose are returned.
func GatewayExposeModes(cfg *ProjectConfig) map[string]string {
	out := map[string]string{}
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
		mode := parseGatewayExposeMode(rawRule)
		if mode == GatewayModeDisable {
			continue
		}
		out[name] = mode
	}
	return out
}

func parseGatewayExposeMode(raw any) string {
	if raw == nil {
		return GatewayModeReverseProxy
	}
	if mode, ok := normalizeGatewayModeValue(raw); ok {
		return mode
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return GatewayModeDisable
	}
	modeRaw, ok := m["mode"]
	if !ok || modeRaw == nil {
		return GatewayModeReverseProxy
	}
	mode, ok := normalizeGatewayModeValue(modeRaw)
	if !ok {
		return GatewayModeDisable
	}
	return mode
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
