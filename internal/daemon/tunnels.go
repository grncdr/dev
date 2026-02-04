package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dev-mode/internal/config"
	"dev-mode/internal/gateway"
)

type managedTunnel struct {
	req        TunnelRequest
	cancel     context.CancelFunc
	status     string
	publicHost string
	lastError  string
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
				Slug:       running.req.Slug,
				Label:      running.req.Label,
				GatewayURL: running.req.GatewayURL,
				PublicHost: running.publicHost,
				Project:    running.req.Project,
				Status:     running.status,
				LastError:  running.lastError,
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

	gatewayClient, gatewayTLS, err := gatewayMTLSClient(req.GatewayURL, s.daemonConfig)
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
		AgentID:        fmt.Sprintf("dev-mode-%d", time.Now().UnixNano()),
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
		Slug:       req.Slug,
		Label:      req.Label,
		GatewayURL: req.GatewayURL,
		PublicHost: mt.publicHost,
		Project:    req.Project,
		Status:     mt.status,
	}, nil
}

func gatewayMTLSClient(gatewayURL string, daemonCfg *config.DaemonConfig) (*http.Client, *tls.Config, error) {
	parsed, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil {
		return nil, nil, err
	}
	if parsed.Scheme != "https" {
		return nil, nil, nil
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, nil, errors.New("gateway URL host is required")
	}
	credDir, err := gatewayCredentialDir(host)
	if err != nil {
		return nil, nil, err
	}
	keyPath := filepath.Join(credDir, "client-key.pem")
	certPath := filepath.Join(credDir, "client.pem")
	caPath := filepath.Join(credDir, "ca.pem")
	if _, err := os.Stat(keyPath); err != nil {
		return nil, nil, fmt.Errorf("missing gateway credentials for %s (run dev-mode gateway login --gateway-url %s)", host, gatewayURL)
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load gateway client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway CA certificate: %w", err)
	}
	// Start from system roots so public CA-signed gateway certs continue to verify.
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if ok := rootCAs.AppendCertsFromPEM(caPEM); !ok {
		return nil, nil, errors.New("invalid gateway CA certificate")
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   host,
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
	return client, tlsCfg, nil
}

func gatewayCredentialDir(host string) (string, error) {
	stateDir, err := config.ResolveStateDir(nil)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "gateway", "agent-credentials", host), nil
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
		Slug:       mt.req.Slug,
		Label:      mt.req.Label,
		GatewayURL: mt.req.GatewayURL,
		PublicHost: mt.publicHost,
		Project:    mt.req.Project,
		Status:     "stopped",
	}, nil
}

func (s *Server) tunnelsStatus() *TunnelsResponse {
	s.tunnelMu.Lock()
	defer s.tunnelMu.Unlock()
	resp := &TunnelsResponse{Tunnels: []TunnelStatus{}}
	for _, mt := range s.tunnels {
		resp.Tunnels = append(resp.Tunnels, TunnelStatus{
			Slug:       mt.req.Slug,
			Label:      mt.req.Label,
			GatewayURL: mt.req.GatewayURL,
			PublicHost: mt.publicHost,
			Project:    mt.req.Project,
			Status:     mt.status,
			LastError:  mt.lastError,
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
