package router

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrNoProxyMatcherMatched = errors.New("no proxy matcher matched")
	ErrNoWorktreesAvailable  = errors.New("no worktrees available for proxy routing")
	ErrHostNotMapped         = errors.New("host does not map to a known worktree")
	ErrWorktreeNotMapped     = errors.New("worktree does not map to a known route")
)

type SubdomainKind int

const (
	SubdomainBase SubdomainKind = iota
	SubdomainWildcard
	SubdomainExplicit
)

type Matcher struct {
	Process   string
	Subdomain string
	Kind      SubdomainKind
	Path      string
	Match     string
	Priority  int
	TCPListen int
	Singleton bool
}

type WorktreeInput struct {
	RuntimeKey string
	Slug       string
	RepoPath   string
	Labels     []string
	Matchers   []Matcher
}

type worktreeRoute struct {
	runtimeKey string
	slug       string
	repoPath   string
	labels     []string
	matchers   []Matcher
}

type Router struct {
	mu        sync.RWMutex
	apexZone  string
	worktrees map[string]worktreeRoute
	labelKeys map[string]map[string]struct{}
}

func New(apexZone string) *Router {
	r := &Router{
		worktrees: map[string]worktreeRoute{},
		labelKeys: map[string]map[string]struct{}{},
	}
	r.SetApexZone(apexZone)
	return r
}

func (r *Router) SetApexZone(apexZone string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apexZone = normalizeApexZone(apexZone)
}

func (r *Router) UpsertWorktree(in WorktreeInput) {
	r.mu.Lock()
	defer r.mu.Unlock()

	runtimeKey := strings.TrimSpace(in.RuntimeKey)
	if runtimeKey == "" {
		return
	}
	if previous, ok := r.worktrees[runtimeKey]; ok {
		r.removeFromLabelIndexLocked(previous.labels, runtimeKey)
	}

	labels := normalizeLabels(in.Labels)
	route := worktreeRoute{
		runtimeKey: runtimeKey,
		slug:       strings.TrimSpace(in.Slug),
		repoPath:   strings.TrimSpace(in.RepoPath),
		labels:     labels,
		matchers:   append([]Matcher(nil), in.Matchers...),
	}
	r.worktrees[runtimeKey] = route
	r.addToLabelIndexLocked(labels, runtimeKey)
}

func (r *Router) RemoveWorktree(runtimeKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.TrimSpace(runtimeKey)
	if key == "" {
		return
	}
	route, ok := r.worktrees[key]
	if !ok {
		return
	}
	delete(r.worktrees, key)
	r.removeFromLabelIndexLocked(route.labels, key)
}

func (r *Router) Resolve(host, path string) (string, *Matcher, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.worktrees) == 0 {
		return "", nil, ErrNoWorktreesAvailable
	}
	labels, err := parseHostLabels(host, r.apexZone)
	if err != nil {
		return "", nil, err
	}

	var selected worktreeRoute
	subdomain := ""
	found := false
	for i := 0; i < len(labels); i++ {
		label := strings.Join(labels[i:], ".")
		keys := r.labelKeys[label]
		if len(keys) == 0 {
			continue
		}
		if len(keys) > 1 {
			return "", nil, r.ambiguousLabelErr(label, keys)
		}
		var key string
		for candidate := range keys {
			key = candidate
		}
		route, ok := r.worktrees[key]
		if !ok {
			continue
		}
		selected = route
		subdomain = strings.Join(labels[:i], ".")
		found = true
		break
	}
	if !found {
		return "", nil, ErrHostNotMapped
	}

	matcher, err := resolveFromRoute(selected, subdomain, path)
	if err != nil {
		return "", nil, err
	}
	return selected.runtimeKey, matcher, nil
}

func (r *Router) ResolveWithinWorktree(runtimeKey, host, path string) (*Matcher, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.worktrees) == 0 {
		return nil, ErrNoWorktreesAvailable
	}
	key := strings.TrimSpace(runtimeKey)
	route, ok := r.worktrees[key]
	if !ok {
		return nil, ErrWorktreeNotMapped
	}
	labels, err := parseHostLabels(host, r.apexZone)
	if err != nil {
		return nil, err
	}
	subdomain, ok := subdomainForRouteLabels(labels, route.labels)
	if !ok {
		return nil, ErrHostNotMapped
	}
	return resolveFromRoute(route, subdomain, path)
}

func (r *Router) ambiguousLabelErr(label string, keys map[string]struct{}) error {
	entries := make([]string, 0, len(keys))
	for key := range keys {
		route, ok := r.worktrees[key]
		if !ok {
			continue
		}
		entries = append(entries, fmt.Sprintf("%s:%s (%s)", route.slug, label, route.repoPath))
	}
	sort.Strings(entries)
	return fmt.Errorf("host label %q matches multiple worktrees (%s)", label, strings.Join(entries, ", "))
}

func (r *Router) addToLabelIndexLocked(labels []string, runtimeKey string) {
	for _, label := range labels {
		keys, ok := r.labelKeys[label]
		if !ok {
			keys = map[string]struct{}{}
			r.labelKeys[label] = keys
		}
		keys[runtimeKey] = struct{}{}
	}
}

func (r *Router) removeFromLabelIndexLocked(labels []string, runtimeKey string) {
	for _, label := range labels {
		keys, ok := r.labelKeys[label]
		if !ok {
			continue
		}
		delete(keys, runtimeKey)
		if len(keys) == 0 {
			delete(r.labelKeys, label)
			continue
		}
		r.labelKeys[label] = keys
	}
}

func normalizeApexZone(apexZone string) string {
	zone := strings.TrimSpace(strings.ToLower(apexZone))
	if zone == "" {
		return "localhost"
	}
	zone = strings.TrimPrefix(zone, ".")
	if zone == "" {
		return "localhost"
	}
	return zone
}

func parseHostLabels(host, apexZone string) ([]string, error) {
	normalized := normalizeHost(host)
	suffix := "." + normalizeApexZone(apexZone)
	if !strings.HasSuffix(normalized, suffix) {
		return nil, errors.New("host does not match apex zone")
	}
	rest := strings.TrimSuffix(normalized, suffix)
	rest = strings.TrimSuffix(rest, ".")
	if rest == "" {
		return nil, errors.New("missing worktree slug")
	}
	return strings.Split(rest, "."), nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, ":")
	}
	return strings.TrimSuffix(host, ".")
}

func normalizeLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		candidate := strings.TrimSpace(strings.ToLower(label))
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

func subdomainForRouteLabels(hostLabels, routeLabels []string) (string, bool) {
	if len(hostLabels) == 0 || len(routeLabels) == 0 {
		return "", false
	}
	routeSet := make(map[string]struct{}, len(routeLabels))
	for _, label := range routeLabels {
		routeSet[label] = struct{}{}
	}
	for i := 0; i < len(hostLabels); i++ {
		label := strings.Join(hostLabels[i:], ".")
		if _, ok := routeSet[label]; !ok {
			continue
		}
		return strings.Join(hostLabels[:i], "."), true
	}
	return "", false
}

func resolveFromRoute(route worktreeRoute, subdomain, path string) (*Matcher, error) {
	best, ok := SelectMatcher(route.matchers, subdomain, path)
	if !ok {
		return nil, ErrNoProxyMatcherMatched
	}
	matched := *best
	return &matched, nil
}

func SelectMatcher(matchers []Matcher, subdomain, path string) (*Matcher, bool) {
	var best *Matcher
	bestKind := SubdomainKind(-1)
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
		ok, pathLen := MatchPath(path, matcher.Path, matcher.Match)
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

func matchesSubdomain(matcher *Matcher, subdomain string) bool {
	switch matcher.Kind {
	case SubdomainBase:
		return subdomain == ""
	case SubdomainWildcard:
		return subdomain != ""
	case SubdomainExplicit:
		return subdomain == matcher.Subdomain
	default:
		return false
	}
}

func MatchPath(requestPath, pattern, mode string) (bool, int) {
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
