package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"dev/internal/agent"
	"dev/internal/config"
	"dev/internal/router"
	"dev/internal/worktree"
)

type tunnelProxyRoute struct {
	LocalHost    string
	AuthUsername string
	AuthPassword string
}

var errGatewayProcessNotExposed = router.ErrGatewayProcessNotExposed

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
		routeSlug, ok := s.tunnelRouteSlug(mt)
		if !ok {
			continue
		}
		apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
		if apex == "" {
			apex = "localhost"
		}
		route := tunnelProxyRoute{
			AuthUsername: mt.AuthUsername,
			AuthPassword: mt.AuthPassword,
		}
		if idx == 0 {
			route.LocalHost = routeSlug + "." + apex
			return route, true
		}
		route.LocalHost = labels[0] + "." + routeSlug + "." + apex
		return route, true
	}
	return tunnelProxyRoute{}, false
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

func (s *Server) tunnelRouteSlug(mt *agent.TunnelStatus) (string, bool) {
	if mt == nil {
		return "", false
	}
	slug := strings.TrimSpace(mt.Slug)
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
	if s.manager == nil || s.manager.router == nil {
		return "", "", "", "", "", "", "", errors.New("proxy router unavailable")
	}
	resolved, err := s.manager.router.Resolve(host, path, fromGateway)
	if err != nil {
		return "", "", "", "", "", "", "", err
	}

	targetSlug = resolved.Slug
	targetPath = resolved.RepoPath
	if resolved.Singleton {
		mainSlug, err := resolveMainWorktreeSlug(resolved.RepoPath)
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
		targetSlug = mainSlug
		mainPath, err := worktree.ResolveMainPathInDir(resolved.RepoPath)
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
		targetPath = mainPath
	}

	s.manager.beginProxySessionFromDir(targetSlug, targetPath, resolved.Process)
	network, address, err = s.manager.EnsureProcessForTargetFromDir(targetSlug, targetPath, resolved.Process)
	if err != nil {
		s.manager.endProxySessionFromDir(targetSlug, targetPath, resolved.Process)
		return "", "", "", "", "", "", "", err
	}
	return network, address, resolved.Process, resolved.GatewayMode, resolved.GatewayDebugLog, targetSlug, targetPath, nil
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
			if s == nil || s.config == nil || strings.TrimSpace(s.mainPath) == "" {
				return nil, "", err
			}
			mainSlug := "main"
			if configured, normalizeErr := worktree.NormalizeIdentifierSegment(s.config.Project.MainSlug); normalizeErr == nil && configured != "" {
				mainSlug = configured
			}
			candidate := strings.TrimSpace(strings.ToLower(slug))
			if candidate == "main" || candidate == mainSlug {
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
