package router

import (
	"reflect"
	"strings"

	"dev/internal/config"
)

func ParseMatchers(cfg *config.ProjectConfig) []Matcher {
	if cfg == nil {
		return nil
	}
	matchers := []Matcher{}
	for name, proc := range cfg.Processes {
		rawProxy, ok := proc["proxy"]
		if !ok || rawProxy == nil {
			continue
		}
		singleton := false
		if rawSingleton, ok := proc["singleton"].(bool); ok {
			singleton = rawSingleton
		}
		matchers = append(matchers, parseMatchers(name, rawProxy, singleton)...)
	}
	return matchers
}

func parseMatchers(process string, raw any, singleton bool) []Matcher {
	raw = normalizeProxyConfigValue(raw)
	switch typed := raw.(type) {
	case []any:
		out := []Matcher{}
		for _, item := range typed {
			out = append(out, parseMatchers(process, item, singleton)...)
		}
		return out
	case map[string]any:
		return parseMatchersFromMap(process, typed, singleton)
	default:
		return nil
	}
}

func normalizeProxyConfigValue(raw any) any {
	if raw == nil {
		return nil
	}
	val := reflect.ValueOf(raw)
	switch val.Kind() {
	case reflect.Map:
		out := map[string]any{}
		for _, key := range val.MapKeys() {
			if key.Kind() != reflect.String {
				continue
			}
			out[key.String()] = normalizeProxyConfigValue(val.MapIndex(key).Interface())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, val.Len())
		for i := 0; i < val.Len(); i++ {
			out[i] = normalizeProxyConfigValue(val.Index(i).Interface())
		}
		return out
	default:
		return raw
	}
}

func parseMatchersFromMap(process string, raw map[string]any, singleton bool) []Matcher {
	subdomains := parseSubdomainValues(raw["subdomain"], raw["subdomains"])
	if len(subdomains) == 0 {
		return nil
	}
	path := "/"
	if rawPath, ok := raw["path"].(string); ok && rawPath != "" {
		path = rawPath
	}
	match := "prefix"
	if rawMatch, ok := raw["match"].(string); ok && rawMatch != "" {
		match = strings.ToLower(strings.TrimSpace(rawMatch))
	}
	priority, _ := config.ParseInt(raw["priority"])
	tcpListen := 0
	if val, ok := config.ParseInt(raw["tcp_listen"]); ok && val > 0 {
		tcpListen = val
	}
	out := make([]Matcher, 0, len(subdomains))
	for _, sd := range subdomains {
		out = append(out, Matcher{
			Process:   process,
			Subdomain: sd.subdomain,
			Kind:      sd.kind,
			Path:      path,
			Match:     match,
			Priority:  priority,
			TCPListen: tcpListen,
			Singleton: singleton,
		})
	}
	return out
}

type parsedSubdomain struct {
	subdomain string
	kind      SubdomainKind
}

func parseSubdomainValues(rawSingle, rawList any) []parsedSubdomain {
	if rawList != nil {
		switch typed := rawList.(type) {
		case []any:
			out := make([]parsedSubdomain, 0, len(typed))
			for _, item := range typed {
				sd, ok := parseSingleSubdomainValue(item)
				if !ok {
					continue
				}
				out = append(out, sd)
			}
			return dedupeSubdomains(out)
		}
	}
	sd, ok := parseSingleSubdomainValue(rawSingle)
	if !ok {
		return nil
	}
	return []parsedSubdomain{sd}
}

func parseSingleSubdomainValue(raw any) (parsedSubdomain, bool) {
	if raw == nil {
		return parsedSubdomain{subdomain: "", kind: SubdomainBase}, true
	}
	val, ok := raw.(string)
	if !ok {
		return parsedSubdomain{}, false
	}
	val = strings.ToLower(strings.TrimSpace(val))
	if val == "" {
		return parsedSubdomain{subdomain: "", kind: SubdomainBase}, true
	}
	if val == "*" {
		return parsedSubdomain{subdomain: "*", kind: SubdomainWildcard}, true
	}
	return parsedSubdomain{subdomain: val, kind: SubdomainExplicit}, true
}

func dedupeSubdomains(values []parsedSubdomain) []parsedSubdomain {
	seen := map[string]bool{}
	out := make([]parsedSubdomain, 0, len(values))
	for _, v := range values {
		key := string(rune(v.kind)) + ":" + v.subdomain
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}
