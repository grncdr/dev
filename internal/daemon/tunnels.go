package daemon

import (
	"errors"
	"fmt"
	"strings"

	"dev/internal/agent"
	"dev/internal/config"
	"dev/internal/worktree"
)

func (s *Server) openTunnel(req TunnelRequest) (*TunnelStatus, error) {
	if strings.TrimSpace(req.Slug) == "" {
		return nil, errors.New("slug is required")
	}
	if strings.TrimSpace(req.Label) == "" {
		return nil, errors.New("label is required")
	}
	if strings.TrimSpace(req.GatewayURL) == "" {
		return nil, errors.New("gateway_url is required")
	}
	req.AuthUsername = strings.TrimSpace(req.AuthUsername)
	if (req.AuthUsername == "") != (req.AuthPassword == "") {
		return nil, errors.New("auth_username and auth_password must both be set")
	}
	if strings.TrimSpace(req.Path) != "" {
		if _, _, err := worktree.EnsureProjectStateForDir(s.daemonConfig, req.Path); err != nil {
			return nil, err
		}
	}

	localBaseHost, err := s.resolveTunnelLocalBaseHost(req.Slug, req.Path)
	if err != nil {
		return nil, err
	}

	gatewayURL := strings.TrimSpace(req.GatewayURL)
	credentialDir, err := config.ResolveGatewayCredentialDirForURL(s.daemonConfig, gatewayURL)
	if err != nil {
		return nil, err
	}
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.agents == nil {
		s.agents = map[string]*agent.Connection{}
	}
	for url, conn := range s.agents {
		if conn == nil {
			continue
		}
		for _, status := range conn.Statuses() {
			if status.Label == req.Label {
				if !status.Identifier.Equal(req.Identifier) {
					return nil, fmt.Errorf("label %s already in use by %s (gateway %s)", req.Label, status.Identifier, url)
				}
				out := toDaemonTunnelStatus(status)
				return &out, nil
			}
			if status.Identifier.Equal(req.Identifier) {
				return nil, fmt.Errorf("slug %s already has label %s (gateway %s)", req.Slug, status.Label, url)
			}
		}
	}
	conn, ok := s.agents[gatewayURL]
	if !ok || conn == nil {
		conn = agent.NewConnection(gatewayURL, credentialDir)
		s.agents[gatewayURL] = conn
	}
	opened, err := conn.Open(agent.TunnelSpec{
		Identifier:    req.Identifier,
		Label:         req.Label,
		GatewayURL:    req.GatewayURL,
		Name:          req.Name,
		LocalBaseHost: localBaseHost,
		AuthUsername:  req.AuthUsername,
		AuthPassword:  req.AuthPassword,
	}, s.handleTunnelAgentRequest)
	if err != nil {
		if conn.Empty() {
			delete(s.agents, gatewayURL)
		}
		return nil, err
	}
	resp := toDaemonTunnelStatus(opened)
	return &resp, nil
}

func (s *Server) closeTunnel(req TunnelRequest) (*TunnelStatus, error) {
	label := strings.TrimSpace(req.Label)
	if label == "" && strings.TrimSpace(req.Slug) == "" {
		return nil, errors.New("label or slug is required")
	}

	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.agents == nil {
		return nil, errors.New("tunnel not found")
	}
	for gatewayURL, conn := range s.agents {
		if conn == nil {
			continue
		}
		status, err := conn.Close(label, req.Identifier)
		if err != nil {
			continue
		}
		if conn.Empty() {
			delete(s.agents, gatewayURL)
		}
		resp := toDaemonTunnelStatus(status)
		return &resp, nil
	}
	return nil, errors.New("tunnel not found")
}

func (s *Server) tunnelsStatus() *TunnelsResponse {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	resp := &TunnelsResponse{Tunnels: []TunnelStatus{}}
	for _, conn := range s.agents {
		if conn == nil {
			continue
		}
		for _, status := range conn.Statuses() {
			resp.Tunnels = append(resp.Tunnels, toDaemonTunnelStatus(status))
		}
	}
	return resp
}

func (s *Server) stopAllTunnels() {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	for gatewayURL, conn := range s.agents {
		if conn != nil {
			conn.StopAll()
		}
		delete(s.agents, gatewayURL)
	}
}

func (s *Server) runningTunnelsForResume() []TunnelRequest {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if len(s.agents) == 0 {
		return nil
	}
	out := make([]TunnelRequest, 0, len(s.agents))
	for _, conn := range s.agents {
		if conn == nil {
			continue
		}
		for _, spec := range conn.RunningRequests() {
			// Path is intentionally omitted: by the time a tunnel is running,
			// openTunnel has already persisted the project via EnsureProjectStateForDir,
			// so resolution on resume works without the dir hint.
			req := TunnelRequest{
				Identifier:   spec.Identifier,
				Label:        spec.Label,
				GatewayURL:   spec.GatewayURL,
				Name:         spec.Name,
				AuthUsername: spec.AuthUsername,
				AuthPassword: spec.AuthPassword,
			}
			if strings.TrimSpace(req.Label) == "" || strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.GatewayURL) == "" {
				continue
			}
			out = append(out, req)
		}
	}
	return out
}

func (s *Server) resolveTunnelLocalBaseHost(slug, dirHint string) (string, error) {
	cfg, repoPath, err := s.projectConfigForSlugFromDir(slug, dirHint)
	if err != nil {
		return "", err
	}
	resolvedSlug := slug
	if sameResolvedPath(repoPath, s.mainPath) {
		if mainSlug, err := resolveMainWorktreeSlug(repoPath); err == nil {
			resolvedSlug = mainSlug
		}
	}
	label := worktree.ProxyDNSLabelForSlug(cfg, resolvedSlug)
	apex := strings.TrimPrefix(strings.ToLower(s.projectApexZone()), ".")
	if apex == "" {
		apex = "localhost"
	}
	return label + "." + apex, nil
}

func toDaemonTunnelStatus(status agent.TunnelStatus) TunnelStatus {
	return TunnelStatus{
		Identifier:      status.Identifier,
		Label:           status.Label,
		GatewayURL:      status.GatewayURL,
		PublicHost:      status.PublicHost,
		Status:          status.Status,
		LastError:       status.LastError,
		RegisterStage:   status.RegisterStage,
		RegisterMessage: status.RegisterMessage,
	}
}
