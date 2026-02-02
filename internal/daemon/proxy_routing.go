package daemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"dev-mode/internal/config"
	"dev-mode/internal/worktree"
)

type subdomainMatchKind int

const (
	subdomainBase subdomainMatchKind = iota
	subdomainWildcard
	subdomainExplicit
)

type proxyMatcher struct {
	Process   string
	Subdomain string
	Kind      subdomainMatchKind
	Path      string
	Match     string
	Priority  int
	TCPListen int
	Singleton bool
}

func (s *Server) parseProxyHost(host string) (slug, subdomain string, err error) {
	apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
	if apex == "" {
		apex = "localhost"
	}

	lower := strings.ToLower(host)
	if strings.Contains(lower, ":") {
		lower, _, _ = strings.Cut(lower, ":")
	}

	suffix := "." + apex
	if !strings.HasSuffix(lower, suffix) {
		return "", "", errors.New("host does not match apex zone")
	}

	rest := strings.TrimSuffix(lower, suffix)
	rest = strings.TrimSuffix(rest, ".")
	if rest == "" {
		return "", "", errors.New("missing worktree slug")
	}
	labels := strings.Split(rest, ".")
	switch len(labels) {
	case 1:
		return labels[0], "", nil
	case 2:
		return labels[1], labels[0], nil
	default:
		return "", "", errors.New("expected <slug> or <subdomain>.<slug>")
	}
}

func (s *Server) resolveProxyTarget(host, path string) (network string, address string, err error) {
	requestedSlug, subdomain, err := s.parseProxyHost(host)
	if err != nil {
		return "", "", err
	}

	slug, err := s.resolveRequestedSlug(requestedSlug)
	if err != nil {
		return "", "", err
	}

	cfg, repoPath, err := s.projectConfigForSlug(slug)
	if err != nil {
		return "", "", err
	}

	matchers := processProxyMatchers(cfg)
	best, ok := selectProxyMatcher(matchers, subdomain, path)
	if !ok {
		return "", "", errors.New("no proxy matcher matched")
	}

	targetSlug := slug
	if best.Singleton {
		mainSlug, err := worktree.ResolveMainSlugInDir(repoPath)
		if err != nil {
			return "", "", err
		}
		targetSlug = mainSlug
	}

	return s.manager.EnsureProcessForTarget(targetSlug, best.Process)
}

func (s *Server) resolveRequestedSlug(requestedSlug string) (string, error) {
	if requestedSlug == "" {
		return "", errors.New("missing requested slug")
	}
	mainSlug, err := worktree.ResolveMainSlugInDir(s.mainPath)
	if err != nil {
		return requestedSlug, nil
	}
	mainPath, err := worktree.ResolvePathFromSlugInDir(mainSlug, s.mainPath)
	if err != nil {
		return requestedSlug, nil
	}
	cfgPath := filepath.Join(mainPath, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return requestedSlug, nil
	}
	override := strings.TrimSpace(strings.ToLower(cfg.Project.MainSlug))
	if override != "" && strings.EqualFold(requestedSlug, override) {
		return mainSlug, nil
	}
	return requestedSlug, nil
}

func prefixMatch(requestPath, prefix string) bool {
	if strings.HasSuffix(prefix, "*") {
		raw := strings.TrimSuffix(prefix, "*")
		if raw == "" {
			return true
		}
		return strings.HasPrefix(requestPath, raw)
	}
	if prefix == "/" {
		return true
	}
	if requestPath == prefix {
		return true
	}
	return strings.HasPrefix(requestPath, prefix+"/")
}

func processProxyMatchers(cfg *config.ProjectConfig) []proxyMatcher {
	if cfg == nil {
		return nil
	}
	matchers := []proxyMatcher{}
	for name, proc := range cfg.Processes {
		rawProxy, ok := proc["proxy"]
		if !ok || rawProxy == nil {
			continue
		}
		singleton := false
		if rawSingleton, ok := proc["singleton"].(bool); ok {
			singleton = rawSingleton
		}
		matchers = append(matchers, parseProxyMatchers(name, rawProxy, singleton)...)
	}
	return matchers
}

func (s *Server) projectConfigForSlug(slug string) (*config.ProjectConfig, string, error) {
	repoPath, ok := s.manager.WorktreePath(slug)
	if !ok || repoPath == "" {
		var err error
		repoPath, err = worktree.ResolvePathFromSlug(slug)
		if err != nil {
			if s.config != nil && s.mainPath != "" {
				return s.config, s.mainPath, nil
			}
			return nil, "", err
		}
	}
	cfgPath := filepath.Join(repoPath, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, "", err
	}
	return cfg, repoPath, nil
}

func parseProxyMatchers(process string, raw any, singleton bool) []proxyMatcher {
	raw = normalizeProxyConfigValue(raw)
	switch typed := raw.(type) {
	case []any:
		out := []proxyMatcher{}
		for _, item := range typed {
			out = append(out, parseProxyMatchers(process, item, singleton)...)
		}
		return out
	case map[string]any:
		return parseProxyMatchersFromMap(process, typed, singleton)
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

func parseProxyMatchersFromMap(process string, raw map[string]any, singleton bool) []proxyMatcher {
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
	priority := parsePriority(raw["priority"])
	tcpListen := 0
	if val, ok := asInt(raw["tcp_listen"]); ok && val > 0 {
		tcpListen = val
	}
	out := make([]proxyMatcher, 0, len(subdomains))
	for _, sd := range subdomains {
		out = append(out, proxyMatcher{
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
	kind      subdomainMatchKind
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
		return parsedSubdomain{subdomain: "", kind: subdomainBase}, true
	}
	val, ok := raw.(string)
	if !ok {
		return parsedSubdomain{}, false
	}
	val = strings.ToLower(strings.TrimSpace(val))
	if val == "" {
		return parsedSubdomain{subdomain: "", kind: subdomainBase}, true
	}
	if val == "*" {
		return parsedSubdomain{subdomain: "*", kind: subdomainWildcard}, true
	}
	return parsedSubdomain{subdomain: val, kind: subdomainExplicit}, true
}

func dedupeSubdomains(values []parsedSubdomain) []parsedSubdomain {
	seen := map[string]bool{}
	out := make([]parsedSubdomain, 0, len(values))
	for _, v := range values {
		key := fmt.Sprintf("%d:%s", v.kind, v.subdomain)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func parsePriority(raw any) int {
	switch value := raw.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func selectProxyMatcher(matchers []proxyMatcher, subdomain, path string) (*proxyMatcher, bool) {
	var best *proxyMatcher
	bestKind := subdomainMatchKind(-1)
	bestPathLen := -1
	bestPriority := 0
	for i := range matchers {
		matcher := &matchers[i]
		if !matchesSubdomain(matcher, subdomain) {
			continue
		}
		if matcher.TCPListen > 0 {
			continue
		}
		ok, pathLen := matchPath(path, matcher.Path, matcher.Match)
		if !ok {
			continue
		}
		if matcher.Kind > bestKind {
			best = matcher
			bestKind = matcher.Kind
			bestPathLen = pathLen
			bestPriority = matcher.Priority
			continue
		}
		if matcher.Kind < bestKind {
			continue
		}
		if pathLen > bestPathLen {
			best = matcher
			bestPathLen = pathLen
			bestPriority = matcher.Priority
			continue
		}
		if pathLen < bestPathLen {
			continue
		}
		if matcher.Priority > bestPriority {
			best = matcher
			bestPriority = matcher.Priority
			continue
		}
		if matcher.Priority < bestPriority {
			continue
		}
		if best == nil || matcher.Process < best.Process {
			best = matcher
			bestPriority = matcher.Priority
		}
	}
	return best, best != nil
}

func matchesSubdomain(matcher *proxyMatcher, subdomain string) bool {
	switch matcher.Kind {
	case subdomainBase:
		return subdomain == ""
	case subdomainWildcard:
		return subdomain != ""
	case subdomainExplicit:
		return subdomain == matcher.Subdomain
	default:
		return false
	}
}

func matchPath(requestPath, pattern, mode string) (bool, int) {
	if pattern == "" {
		pattern = "/"
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "prefix"
	}
	if mode == "prefix" && pattern != "/" {
		pattern = strings.TrimRight(pattern, "/")
		if pattern == "" {
			pattern = "/"
		}
	}
	if mode == "exact" {
		if requestPath != pattern {
			return false, 0
		}
		return true, len(pattern)
	}
	if strings.HasSuffix(pattern, "*") {
		raw := strings.TrimSuffix(pattern, "*")
		if raw == "" {
			return true, 0
		}
		if strings.HasPrefix(requestPath, raw) {
			return true, len(raw)
		}
		return false, 0
	}
	if !prefixMatch(requestPath, pattern) {
		return false, 0
	}
	return true, len(pattern)
}

func (k subdomainMatchKind) String() string {
	switch k {
	case subdomainBase:
		return "base"
	case subdomainWildcard:
		return "wildcard"
	case subdomainExplicit:
		return "explicit"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}
