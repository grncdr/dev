package daemon

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dev/internal/agent"
	"dev/internal/gateway"
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
	if strings.TrimSpace(req.Upstream) == "" {
		req.Upstream = s.localProxyUpstreamURL()
	}
	if req.Upstream == "" {
		return nil, errors.New("no proxy upstream available")
	}

	localBaseHost, err := s.resolveTunnelLocalBaseHost(req.Slug)
	if err != nil {
		return nil, err
	}

	gatewayURL := strings.TrimSpace(req.GatewayURL)
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
				out := toDaemonTunnelStatus(status)
				return &out, nil
			}
			if status.Slug == req.Slug && status.Label != req.Label {
				return nil, fmt.Errorf("slug %s already has label %s (gateway %s)", req.Slug, status.Label, url)
			}
		}
	}
	conn, ok := s.agents[gatewayURL]
	if !ok || conn == nil {
		conn = agent.NewConnection(gatewayURL)
		s.agents[gatewayURL] = conn
	}

	gatewayClient, gatewayTLS, err := gateway.MTLSClientForGatewayURL(req.GatewayURL, s.daemonConfig)
	if err != nil {
		return nil, err
	}
	opened, err := conn.Open(agent.TunnelSpec{
		Slug:          req.Slug,
		Label:         req.Label,
		GatewayURL:    req.GatewayURL,
		Project:       req.Project,
		Name:          req.Name,
		UpstreamURL:   req.Upstream,
		LocalBaseHost: localBaseHost,
		AuthUsername:  req.AuthUsername,
		AuthPassword:  req.AuthPassword,
	}, gatewayClient, gatewayTLS, tunnelHTTPClient(req.Upstream))
	if err != nil {
		if conn.Empty() {
			delete(s.agents, gatewayURL)
		}
		return nil, err
	}
	resp := toDaemonTunnelStatus(opened)
	return &resp, nil
}

func tunnelHTTPClient(upstream string) *http.Client {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil
	}
	if u.Scheme != "https" {
		return nil
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // local daemon->proxy TLS only
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

func (s *Server) closeTunnel(req TunnelRequest) (*TunnelStatus, error) {
	label := strings.TrimSpace(req.Label)
	slug := strings.TrimSpace(req.Slug)
	if label == "" && slug == "" {
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
		status, err := conn.Close(label, slug)
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
			req := TunnelRequest{
				Slug:         spec.Slug,
				Label:        spec.Label,
				GatewayURL:   spec.GatewayURL,
				Project:      spec.Project,
				Name:         spec.Name,
				Upstream:     spec.UpstreamURL,
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

func (s *Server) resolveTunnelLocalBaseHost(slug string) (string, error) {
	cfg, repoPath, err := s.projectConfigForSlug(slug)
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
		Slug:            status.Slug,
		Label:           status.Label,
		GatewayURL:      status.GatewayURL,
		PublicHost:      status.PublicHost,
		Project:         status.Project,
		Status:          status.Status,
		LastError:       status.LastError,
		RegisterStage:   status.RegisterStage,
		RegisterMessage: status.RegisterMessage,
	}
}

func (s *Server) localProxyUpstreamURL() string {
	httpAddr, httpsAddr := proxyListenAddrs(s.daemonConfig)
	if httpsAddr != "" {
		if addr, ok := loopbackAddr(httpsAddr); ok {
			return "https://" + addr
		}
	}
	if httpAddr != "" {
		if addr, ok := loopbackAddr(httpAddr); ok {
			return "http://" + addr
		}
	}
	return ""
}

func loopbackAddr(addr string) (string, bool) {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr, true
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", false
	}
	_ = host
	return net.JoinHostPort("127.0.0.1", port), true
}
