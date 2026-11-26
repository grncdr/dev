package config

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// PublicPathMatcher selects the requests under a public path prefix that skip
// share auth.
type PublicPathMatcher struct {
	// Any matches every request, WebSocket upgrades included ("*").
	Any bool
	// WebSocket matches WebSocket upgrade requests ("websocket").
	WebSocket bool
	// Methods matches plain (non-upgrade) requests by HTTP method. GET also
	// matches HEAD.
	Methods map[string]bool
}

// PublicPaths maps normalized path prefixes to the requests under them that
// skip share auth. A prefix matches on segment boundaries: "/mcp" covers "/mcp"
// and "/mcp/x" but not "/mcpx"; "/" covers every path.
type PublicPaths map[string]PublicPathMatcher

var publicPathMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
	http.MethodConnect: true, http.MethodTrace: true,
}

// Allows reports whether a request may skip share auth. websocket is true for
// a WebSocket upgrade request. A request is public when any matching prefix
// allows it.
func (p PublicPaths) Allows(reqPath, method string, websocket bool) bool {
	method = strings.ToUpper(method)
	for prefix, matcher := range p {
		if !pathUnderPrefix(reqPath, prefix) {
			continue
		}
		switch {
		case matcher.Any:
			return true
		case websocket:
			if matcher.WebSocket {
				return true
			}
		case matcher.Methods[method], method == http.MethodHead && matcher.Methods[http.MethodGet]:
			return true
		}
	}
	return false
}

func pathUnderPrefix(reqPath, prefix string) bool {
	if !strings.HasPrefix(reqPath, "/") {
		return false
	}
	if prefix == "/" || reqPath == prefix {
		return true
	}
	return strings.HasPrefix(reqPath, prefix+"/")
}

// withWebSocketPaths adds `websocket_paths` entries as prefixes that allow
// WebSocket upgrades.
func (p PublicPaths) withWebSocketPaths(paths []string) PublicPaths {
	if len(paths) == 0 {
		return p
	}
	if p == nil {
		p = PublicPaths{}
	}
	for _, path := range paths {
		key := normalizePublicPathKey(path)
		matcher := p[key]
		matcher.WebSocket = true
		p[key] = matcher
	}
	return p
}

func normalizePublicPathKey(path string) string {
	if path == "/" {
		return path
	}
	return strings.TrimRight(path, "/")
}

// parseGatewayPublicPaths parses a `public_paths` table. It returns nil when the
// table is absent and an error for any invalid entry.
func parseGatewayPublicPaths(raw any) (PublicPaths, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := normalizeStringMap(raw)
	if !ok {
		return nil, errors.New("must be a table of path prefixes")
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := PublicPaths{}
	for _, path := range keys {
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("%q: path must start with /", path)
		}
		matcher, err := parsePublicPathMatcher(m[path])
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		out[normalizePublicPathKey(path)] = matcher
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func parsePublicPathMatcher(raw any) (PublicPathMatcher, error) {
	var values []string
	if s, ok := raw.(string); ok {
		values = []string{s}
	} else {
		val := reflect.ValueOf(raw)
		if raw == nil || (val.Kind() != reflect.Slice && val.Kind() != reflect.Array) {
			return PublicPathMatcher{}, errors.New(`value must be "*", "websocket", an HTTP method, or a list of them`)
		}
		for i := 0; i < val.Len(); i++ {
			s, ok := val.Index(i).Interface().(string)
			if !ok {
				return PublicPathMatcher{}, errors.New("list entries must be strings")
			}
			values = append(values, s)
		}
	}
	if len(values) == 0 {
		return PublicPathMatcher{}, errors.New(`list must not be empty (use "*" to allow every request)`)
	}
	var matcher PublicPathMatcher
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch upper := strings.ToUpper(value); {
		case value == "*":
			matcher.Any = true
		case upper == "WEBSOCKET":
			matcher.WebSocket = true
		case publicPathMethods[upper]:
			if matcher.Methods == nil {
				matcher.Methods = map[string]bool{}
			}
			matcher.Methods[upper] = true
		default:
			return PublicPathMatcher{}, fmt.Errorf(`unknown value %q (want "*", "websocket", or an HTTP method)`, value)
		}
	}
	return matcher, nil
}

// validateGatewayPublicPaths reports the first invalid `public_paths` entry in
// [gateway.expose].
func validateGatewayPublicPaths(cfg *ProjectConfig) error {
	if cfg == nil || cfg.Gateway == nil {
		return nil
	}
	exposeMap, ok := normalizeStringMap(cfg.Gateway["expose"])
	if !ok {
		return nil
	}
	names := make([]string, 0, len(exposeMap))
	for name := range exposeMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rule, ok := normalizeStringMap(exposeMap[name])
		if !ok {
			continue
		}
		if _, err := parseGatewayPublicPaths(rule["public_paths"]); err != nil {
			return fmt.Errorf("gateway.expose.%s.public_paths: %w", name, err)
		}
	}
	return nil
}
