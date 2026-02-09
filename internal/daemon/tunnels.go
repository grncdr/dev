package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dev/internal/gateway"
)

type managedTunnel struct {
	req             TunnelRequest
	cancel          context.CancelFunc
	status          string
	publicHost      string
	lastError       string
	registerStage   string
	registerMessage string
}

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
	if strings.TrimSpace(req.Upstream) == "" {
		req.Upstream = s.localProxyUpstreamURL()
	}
	if req.Upstream == "" {
		return nil, errors.New("no proxy upstream available")
	}

	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if s.tunnels == nil {
		s.tunnels = map[string]*managedTunnel{}
	}
	for label, running := range s.tunnels {
		if label == req.Label {
			return &TunnelStatus{
				Slug:            running.req.Slug,
				Label:           running.req.Label,
				GatewayURL:      running.req.GatewayURL,
				PublicHost:      running.publicHost,
				Project:         running.req.Project,
				Status:          running.status,
				LastError:       running.lastError,
				RegisterStage:   running.registerStage,
				RegisterMessage: running.registerMessage,
			}, nil
		}
		if running.req.Slug == req.Slug && label != req.Label {
			return nil, fmt.Errorf("slug %s already has label %s", req.Slug, label)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	mt := &managedTunnel{
		req:    req,
		cancel: cancel,
		status: "connecting",
	}
	s.tunnels[req.Label] = mt

	gatewayClient, gatewayTLS, err := gateway.MTLSClientForGatewayURL(req.GatewayURL, s.daemonConfig)
	if err != nil {
		delete(s.tunnels, req.Label)
		return nil, err
	}

	agent := &gateway.Agent{
		GatewayURL:     req.GatewayURL,
		UpstreamURL:    req.Upstream,
		Project:        req.Project,
		Slug:           req.Slug,
		Label:          req.Label,
		AgentID:        fmt.Sprintf("dev-%d", time.Now().UnixNano()),
		Name:           req.Name,
		RetryDelay:     500 * time.Millisecond,
		GatewayClient:  gatewayClient,
		TLSConfig:      gatewayTLS,
		UpstreamClient: tunnelHTTPClient(req.Upstream),
		OnConnected: func() {
			s.tunnelMu.Lock()
			defer s.tunnelMu.Unlock()
			if cur, ok := s.tunnels[req.Label]; ok {
				cur.status = "connected"
				cur.lastError = ""
			}
		},
		OnRegistered: func(publicHost string) {
			s.tunnelMu.Lock()
			defer s.tunnelMu.Unlock()
			if cur, ok := s.tunnels[req.Label]; ok {
				cur.publicHost = strings.TrimSpace(publicHost)
				cur.registerStage = "register_complete"
				cur.registerMessage = "gateway registration complete"
			}
		},
		OnRegisterProgress: func(stage, message string) {
			s.tunnelMu.Lock()
			defer s.tunnelMu.Unlock()
			if cur, ok := s.tunnels[req.Label]; ok {
				cur.registerStage = strings.TrimSpace(stage)
				cur.registerMessage = strings.TrimSpace(message)
			}
		},
		OnDisconnected: func(err error) {
			s.tunnelMu.Lock()
			defer s.tunnelMu.Unlock()
			if cur, ok := s.tunnels[req.Label]; ok {
				cur.status = "connecting"
				if err != nil {
					cur.lastError = err.Error()
				}
			}
		},
	}
	go func() {
		err := agent.Run(ctx)
		s.tunnelMu.Lock()
		defer s.tunnelMu.Unlock()
		cur, ok := s.tunnels[req.Label]
		if !ok {
			return
		}
		if err != nil {
			cur.status = "error"
			cur.lastError = err.Error()
			return
		}
		cur.status = "stopped"
	}()

	return &TunnelStatus{
		Slug:            req.Slug,
		Label:           req.Label,
		GatewayURL:      req.GatewayURL,
		PublicHost:      mt.publicHost,
		Project:         req.Project,
		Status:          mt.status,
		RegisterStage:   mt.registerStage,
		RegisterMessage: mt.registerMessage,
	}, nil
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

	if s.tunnels == nil {
		return nil, errors.New("tunnel not found")
	}
	if label == "" {
		for key, tunnel := range s.tunnels {
			if tunnel.req.Slug == slug {
				label = key
				break
			}
		}
	}
	mt, ok := s.tunnels[label]
	if !ok {
		return nil, errors.New("tunnel not found")
	}
	mt.cancel()
	delete(s.tunnels, label)
	return &TunnelStatus{
		Slug:            mt.req.Slug,
		Label:           mt.req.Label,
		GatewayURL:      mt.req.GatewayURL,
		PublicHost:      mt.publicHost,
		Project:         mt.req.Project,
		Status:          "stopped",
		RegisterStage:   mt.registerStage,
		RegisterMessage: mt.registerMessage,
	}, nil
}

func (s *Server) tunnelsStatus() *TunnelsResponse {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	resp := &TunnelsResponse{Tunnels: []TunnelStatus{}}
	for _, mt := range s.tunnels {
		resp.Tunnels = append(resp.Tunnels, TunnelStatus{
			Slug:            mt.req.Slug,
			Label:           mt.req.Label,
			GatewayURL:      mt.req.GatewayURL,
			PublicHost:      mt.publicHost,
			Project:         mt.req.Project,
			Status:          mt.status,
			LastError:       mt.lastError,
			RegisterStage:   mt.registerStage,
			RegisterMessage: mt.registerMessage,
		})
	}
	return resp
}

func (s *Server) stopAllTunnels() {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	for label, mt := range s.tunnels {
		mt.cancel()
		delete(s.tunnels, label)
	}
}

func (s *Server) runningTunnelsForResume() []TunnelRequest {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	if len(s.tunnels) == 0 {
		return nil
	}
	out := make([]TunnelRequest, 0, len(s.tunnels))
	for _, mt := range s.tunnels {
		if mt == nil {
			continue
		}
		req := mt.req
		if strings.TrimSpace(req.Label) == "" || strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.GatewayURL) == "" {
			continue
		}
		out = append(out, req)
	}
	return out
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
