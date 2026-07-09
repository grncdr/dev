package daemon

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"dev/internal/config"
	"dev/internal/router"
	"dev/internal/worktree"
)

func (s *Server) routingStatusForWorktree(slug, project, dirHint string) WorktreeRouting {
	cfg, _, err := s.projectConfigForWorktreeStatus(slug, project, dirHint)
	if err != nil || cfg == nil {
		return WorktreeRouting{}
	}

	routeSlug := localProxyRouteSlug(slug, cfg)
	localByProcess := localProxyRoutesByProcess(router.ParseMatchers(cfg), routeSlug, s.projectApexZone())
	tunnel := s.tunnelStatusForWorktree(worktree.Identifier{Project: project, Slug: slug})

	gatewayURL := strings.TrimSpace(gatewayURLFromConfig(cfg))
	gatewayStatus := ""
	if tunnel != nil {
		gatewayURL = strings.TrimSpace(tunnel.GatewayURL)
		gatewayStatus = tunnel.Status
	}

	routing := WorktreeRouting{
		Local:         localByProcess,
		Gateway:       gatewayRoutesByProcess(localByProcess, processGatewayExposure(cfg), tunnel),
		GatewayURL:    gatewayURL,
		GatewayStatus: gatewayStatus,
	}
	return routing
}

func (s *Server) projectConfigForWorktreeStatus(slug, project, dirHint string) (*config.ProjectConfig, string, error) {
	path, err := s.manager.resolveWorktreePath(slug, project, dirHint)
	if err != nil {
		return nil, "", err
	}
	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

func localProxyRouteSlug(slug string, cfg *config.ProjectConfig) string {
	return worktree.ProxyDNSLabelForSlug(cfg, slug)
}

func localProxyRoutesByProcess(matchers []router.Matcher, routeSlug, apexZone string) map[string][]string {
	out := map[string][]string{}
	zone := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(apexZone)), ".")
	if zone == "" {
		zone = "localhost"
	}
	for _, matcher := range matchers {
		if matcher.Process == "" {
			continue
		}
		if matcher.TCPListen > 0 {
			out[matcher.Process] = append(out[matcher.Process], fmt.Sprintf("tcp://127.0.0.1:%d", matcher.TCPListen))
			continue
		}
		path := matcher.Path
		if path == "" {
			path = "/"
		}
		host := fmt.Sprintf("%s.%s", routeSlug, zone)
		switch matcher.Kind {
		case router.SubdomainWildcard:
			host = fmt.Sprintf("*.%s.%s", routeSlug, zone)
		case router.SubdomainExplicit:
			if matcher.Subdomain != "" {
				host = fmt.Sprintf("%s.%s.%s", matcher.Subdomain, routeSlug, zone)
			}
		}
		out[matcher.Process] = append(out[matcher.Process], fmt.Sprintf("https://%s%s", host, path))
	}
	for process, routes := range out {
		sort.Strings(routes)
		out[process] = dedupeStrings(routes)
	}
	return out
}

func processGatewayExposure(cfg *config.ProjectConfig) map[string]bool {
	out := map[string]bool{}
	for process := range config.GatewayExposeRules(cfg) {
		out[process] = true
	}
	return out
}

func gatewayRoutesByProcess(localByProcess map[string][]string, exposedByProcess map[string]bool, tunnel *TunnelStatus) map[string][]string {
	out := map[string][]string{}
	if tunnel == nil || tunnel.Status != "connected" {
		return out
	}
	gatewayHost := strings.TrimSpace(tunnel.PublicHost)
	label := strings.TrimSpace(tunnel.Label)
	if gatewayHost == "" || label == "" {
		return out
	}
	for process, routes := range localByProcess {
		if !exposedByProcess[process] {
			continue
		}
		for _, route := range routes {
			if !strings.HasPrefix(route, "https://") {
				continue
			}
			hostPath := strings.TrimPrefix(route, "https://")
			host, path, found := strings.Cut(hostPath, "/")
			if !found {
				continue
			}
			parts := strings.Split(host, ".")
			switch {
			case len(parts) == 2:
				host = label + "." + gatewayHost
			case len(parts) >= 3:
				sub := strings.Join(parts[:len(parts)-2], ".")
				host = sub + "." + label + "." + gatewayHost
			default:
				continue
			}
			out[process] = append(out[process], "https://"+host+"/"+path)
		}
		sort.Strings(out[process])
		out[process] = dedupeStrings(out[process])
	}
	return out
}

func (s *Server) tunnelStatusForWorktree(target worktree.Identifier) *TunnelStatus {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if len(s.agents) == 0 {
		return nil
	}
	var selected *TunnelStatus
	for _, conn := range s.agents {
		if conn == nil {
			continue
		}
		candidate := conn.Status(target)
		if candidate == nil {
			continue
		}
		daemonCandidate := &TunnelStatus{
			Identifier: candidate.Identifier,
			Label:      candidate.Label,
			GatewayURL: candidate.GatewayURL,
			PublicHost: candidate.PublicHost,
			Status:     candidate.Status,
			LastError:  candidate.LastError,
		}
		if selected == nil || (selected.Status != "connected" && daemonCandidate.Status == "connected") {
			selected = daemonCandidate
		}
	}
	return selected
}

func gatewayURLFromConfig(cfg *config.ProjectConfig) string {
	if cfg == nil {
		return ""
	}
	url, _ := cfg.Gateway["url"].(string)
	return strings.TrimSpace(url)
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	last := ""
	for i, value := range values {
		if i == 0 || value != last {
			out = append(out, value)
		}
		last = value
	}
	return out
}
