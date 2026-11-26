package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"dev/internal/agent"
	"dev/internal/worktree"
)

type tunnelProxyRoute struct {
	LocalHost    string
	AuthUsername string
	AuthPassword string
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
	if s.isLocalProxyHost(host) {
		return tunnelProxyRoute{}, false
	}
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
		// LocalBaseHost was resolved from the tunnel's project:slug when it was
		// opened. A slug alone can name worktrees of several projects.
		localBaseHost := strings.TrimSpace(mt.LocalBaseHost)
		if localBaseHost == "" {
			continue
		}
		route := tunnelProxyRoute{
			AuthUsername: mt.AuthUsername,
			AuthPassword: mt.AuthPassword,
		}
		if idx == 0 {
			route.LocalHost = localBaseHost
			return route, true
		}
		route.LocalHost = labels[0] + "." + localBaseHost
		return route, true
	}
	return tunnelProxyRoute{}, false
}

func (s *Server) localProxyHostForTarget(host, targetPath string) (string, bool) {
	host = normalizeProxyHost(host)
	if host == "" || strings.TrimSpace(targetPath) == "" || s == nil || s.manager == nil {
		return "", false
	}
	wt, ok := s.manager.WorktreeByRuntimeKey(runtimeKeyForPath(targetPath))
	if !ok {
		return "", false
	}
	targetLabel := strings.TrimSpace(strings.ToLower(wt.DNSLabel))
	if targetLabel == "" {
		return "", false
	}
	slug, subdomain, err := s.parseProxyHost(host)
	if err != nil || strings.TrimSpace(slug) == "" {
		return "", false
	}
	apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
	if apex == "" {
		apex = "localhost"
	}
	if subdomain == "" {
		return targetLabel + "." + apex, true
	}
	return subdomain + "." + targetLabel + "." + apex, true
}

func (s *Server) activeTunnelForLabel(label string) (*agent.TunnelStatus, bool) {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	for _, conn := range s.agents {
		if conn == nil {
			continue
		}
		status, ok := conn.TunnelForLabel(label)
		if !ok {
			continue
		}
		return &status, true
	}
	return nil, false
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

func (s *Server) isLocalProxyHost(host string) bool {
	apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
	if apex == "" {
		apex = "localhost"
	}
	normalized := normalizeProxyHost(host)
	return normalized == apex || strings.HasSuffix(normalized, "."+apex)
}

func resolveMainWorktreeSlug(mainPath string) (string, error) {
	if configured, ok, err := worktree.ResolveConfiguredMainSlug(mainPath); err != nil {
		return "", err
	} else if ok {
		return configured, nil
	}
	return "main", nil
}
