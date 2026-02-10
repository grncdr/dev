package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"dev/internal/config"
	"dev/internal/worktree"
)

type subdomainMatchKind int

const (
	subdomainBase subdomainMatchKind = iota
	subdomainWildcard
	subdomainExplicit
)

type proxyMatcher struct {
	Process         string
	Subdomain       string
	Kind            subdomainMatchKind
	Path            string
	Match           string
	GatewayMode     string
	GatewayDebugLog string
	Priority        int
	TCPListen       int
	Singleton       bool
}

type tunnelProxyRoute struct {
	LocalHost    string
	RouteHost    string
	AuthUsername string
	AuthPassword string
}

var errGatewayProcessNotExposed = errors.New("gateway traffic not exposed for matched process")

type proxyHostCandidate struct {
	requestedSlug string
	subdomain     string
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

func normalizeProxyHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, ":")
	}
	return strings.TrimSuffix(host, ".")
}

func (s *Server) localProxyHostForTunnelRequest(host string) (string, bool) {
	route, ok := s.localProxyRouteForTunnelRequest(host)
	if !ok {
		return "", false
	}
	return route.LocalHost, true
}

func (s *Server) localProxyRouteForTunnelRequest(host string) (tunnelProxyRoute, bool) {
	labels := strings.Split(normalizeProxyHost(host), ".")
	if len(labels) == 0 {
		return tunnelProxyRoute{}, false
	}

	// Prefer "<subdomain>.<label>.<zone>" over "<label>.<zone>" when both labels happen to be active.
	for _, idx := range []int{1, 0} {
		if idx >= len(labels) {
			continue
		}
		mt, ok := s.activeTunnelForLabel(labels[idx])
		if !ok {
			continue
		}
		routeSlug, ok := s.tunnelRouteSlug(mt)
		if !ok {
			continue
		}
		canonicalSlug := strings.TrimSpace(mt.req.Slug)
		if canonicalSlug == "" {
			continue
		}
		apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
		if apex == "" {
			apex = "localhost"
		}
		route := tunnelProxyRoute{
			AuthUsername: mt.req.AuthUsername,
			AuthPassword: mt.req.AuthPassword,
		}
		if idx == 0 {
			route.LocalHost = routeSlug + "." + apex
			route.RouteHost = canonicalSlug + "." + apex
			return route, true
		}
		route.LocalHost = labels[0] + "." + routeSlug + "." + apex
		route.RouteHost = labels[0] + "." + canonicalSlug + "." + apex
		return route, true
	}
	return tunnelProxyRoute{}, false
}

func (s *Server) activeTunnelForLabel(label string) (*managedTunnel, bool) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.tunnels == nil {
		return nil, false
	}
	mt, ok := s.tunnels[label]
	if !ok || mt == nil {
		return nil, false
	}
	copied := *mt
	return &copied, true
}

func (s *Server) tunnelRouteSlug(mt *managedTunnel) (string, bool) {
	if mt == nil {
		return "", false
	}
	slug := strings.TrimSpace(mt.req.Slug)
	if slug == "" {
		return "", false
	}
	cfg, _, err := s.projectConfigForSlug(slug)
	if err != nil || cfg == nil {
		return slug, true
	}
	return worktree.ProxyDNSLabelForSlug(cfg, slug), true
}

func sameResolvedPath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	infoA, errA := os.Stat(resolvedA)
	infoB, errB := os.Stat(resolvedB)
	if errA == nil && errB == nil && os.SameFile(infoA, infoB) {
		return true
	}
	return filepath.Clean(resolvedA) == filepath.Clean(resolvedB)
}

func (s *Server) resolveProxyTarget(host, path string) (network string, address string, process string, gatewayMode string, gatewayDebugLog string, targetSlug string, targetPath string, err error) {
	return s.resolveProxyTargetForRequest(host, path, false)
}

func (s *Server) resolveProxyTargetForRequest(host, path string, fromGateway bool) (network string, address string, process string, gatewayMode string, gatewayDebugLog string, targetSlug string, targetPath string, err error) {
	candidates, err := s.parseProxyHostCandidates(host)
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	lastErr := error(nil)
	for _, candidate := range candidates {
		slug, slugHint, err := s.resolveRequestedSlug(candidate.requestedSlug)
		if err != nil {
			lastErr = err
			continue
		}

		cfg, repoPath, err := s.projectConfigForSlugFromDir(slug, slugHint)
		if err != nil {
			lastErr = err
			continue
		}

		matchers := processProxyMatchers(cfg)
		best, ok := selectProxyMatcher(matchers, candidate.subdomain, path)
		if !ok {
			continue
		}
		if fromGateway && best.GatewayMode == config.GatewayModeDisable {
			return "", "", "", "", "", "", "", errGatewayProcessNotExposed
		}

		targetSlug = slug
		targetPath = repoPath
		if best.Singleton {
			mainSlug, err := resolveMainWorktreeSlug(repoPath)
			if err != nil {
				lastErr = err
				continue
			}
			targetSlug = mainSlug
			mainPath, err := worktree.ResolveMainPathInDir(repoPath)
			if err != nil {
				lastErr = err
				continue
			}
			targetPath = mainPath
		}

		s.manager.beginProxySessionFromDir(targetSlug, targetPath, best.Process)
		network, address, err = s.manager.EnsureProcessForTargetFromDir(targetSlug, targetPath, best.Process)
		if err != nil {
			s.manager.endProxySessionFromDir(targetSlug, targetPath, best.Process)
			lastErr = err
			continue
		}
		return network, address, best.Process, best.GatewayMode, best.GatewayDebugLog, targetSlug, targetPath, nil
	}
	if lastErr != nil {
		return "", "", "", "", "", "", "", lastErr
	}
	return "", "", "", "", "", "", "", errors.New("no proxy matcher matched")
}

func (s *Server) parseProxyHostCandidates(host string) ([]proxyHostCandidate, error) {
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
		return nil, errors.New("host does not match apex zone")
	}
	rest := strings.TrimSuffix(lower, suffix)
	rest = strings.TrimSuffix(rest, ".")
	if rest == "" {
		return nil, errors.New("missing worktree slug")
	}
	labels := strings.Split(rest, ".")
	out := make([]proxyHostCandidate, 0, len(labels))
	for i := 0; i < len(labels); i++ {
		requested := strings.Join(labels[i:], ".")
		subdomain := strings.Join(labels[:i], ".")
		out = append(out, proxyHostCandidate{
			requestedSlug: requested,
			subdomain:     subdomain,
		})
	}
	return out, nil
}

func (s *Server) resolveRequestedSlug(requestedSlug string) (string, string, error) {
	if requestedSlug == "" {
		return "", "", errors.New("missing requested slug")
	}
	mainPath, err := worktree.ResolveMainPathInDir(s.mainPath)
	if err != nil {
		return requestedSlug, "", nil
	}
	cfgPath := filepath.Join(mainPath, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return requestedSlug, "", nil
	}
	if mappedSlug, ok, err := worktree.ResolveSlugForDNSLabel(cfg, s.daemonConfig, requestedSlug); err == nil && ok {
		return mappedSlug, mainPath, nil
	} else if err != nil {
		return "", "", err
	}
	mainSlug, err := resolveMainWorktreeSlug(mainPath)
	if err != nil {
		return requestedSlug, "", nil
	}
	if strings.EqualFold(requestedSlug, "main") {
		return mainSlug, mainPath, nil
	}
	if strings.EqualFold(requestedSlug, mainSlug) {
		return mainSlug, mainPath, nil
	}
	return requestedSlug, "", nil
}

func resolveMainWorktreeSlug(mainPath string) (string, error) {
	if configured, ok, err := worktree.ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if ok {
		return configured, nil
	}
	return "main", nil
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
	gatewayRules := config.GatewayExposeRules(cfg)
	for name, proc := range cfg.Processes {
		rawProxy, ok := proc["proxy"]
		if !ok || rawProxy == nil {
			continue
		}
		singleton := false
		if rawSingleton, ok := proc["singleton"].(bool); ok {
			singleton = rawSingleton
		}
		gatewayMode := config.GatewayModeDisable
		gatewayDebugLog := ""
		if exposedRule, ok := gatewayRules[name]; ok {
			gatewayMode = exposedRule.Mode
			gatewayDebugLog = exposedRule.DebugLog
		}
		matchers = append(matchers, parseProxyMatchers(name, rawProxy, singleton, gatewayMode, gatewayDebugLog)...)
	}
	return matchers
}

func (s *Server) projectConfigForSlug(slug string) (*config.ProjectConfig, string, error) {
	return s.projectConfigForSlugFromDir(slug, "")
}

func (s *Server) projectConfigForSlugFromDir(slug, dirHint string) (*config.ProjectConfig, string, error) {
	repoPath := ""
	ok := false
	if s.manager != nil {
		repoPath, ok = s.manager.WorktreePathFromDir(slug, dirHint)
	}
	if !ok || repoPath == "" {
		repoPath = strings.TrimSpace(dirHint)
		ok = repoPath != ""
	}
	if !ok || repoPath == "" {
		var err error
		repoPath, err = worktree.ResolvePathFromSlugInDir(slug, s.mainPath)
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

func parseProxyMatchers(process string, raw any, singleton bool, gatewayMode string, gatewayDebugLog string) []proxyMatcher {
	raw = normalizeProxyConfigValue(raw)
	switch typed := raw.(type) {
	case []any:
		out := []proxyMatcher{}
		for _, item := range typed {
			out = append(out, parseProxyMatchers(process, item, singleton, gatewayMode, gatewayDebugLog)...)
		}
		return out
	case map[string]any:
		return parseProxyMatchersFromMap(process, typed, singleton, gatewayMode, gatewayDebugLog)
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

func parseProxyMatchersFromMap(process string, raw map[string]any, singleton bool, gatewayMode string, gatewayDebugLog string) []proxyMatcher {
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
	out := make([]proxyMatcher, 0, len(subdomains))
	for _, sd := range subdomains {
		out = append(out, proxyMatcher{
			Process:         process,
			Subdomain:       sd.subdomain,
			Kind:            sd.kind,
			Path:            path,
			Match:           match,
			GatewayMode:     gatewayMode,
			GatewayDebugLog: gatewayDebugLog,
			Priority:        priority,
			TCPListen:       tcpListen,
			Singleton:       singleton,
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
