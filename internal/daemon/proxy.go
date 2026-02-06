package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dev-mode/internal/config"
)

// defaultProxyListenHost returns the default listen host for the proxy.
// On macOS, binding to 0.0.0.0 is required for privileged ports (80/443)
// when using port forwarding from unprivileged ports. On other platforms,
// we default to 127.0.0.1 for security.
func defaultProxyListenHost() string {
	if runtime.GOOS == "darwin" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func defaultProxyHTTPListen() string {
	return defaultProxyListenHost() + ":80"
}

func defaultProxyHTTPSListen() string {
	return defaultProxyListenHost() + ":443"
}

func (s *Server) startProxy() error {
	httpAddr, httpsAddr := proxyListenAddrs(s.daemonConfig)

	if httpsAddr != "" {
		cert, key, caKey, caCert, err := proxyCertPaths()
		if err != nil {
			return err
		}
		certPair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return fmt.Errorf("load proxy cert: %w (run dev-mode cert install)", err)
		}
		certProvider, err := newProxyCertProvider(certPair, caKey, caCert)
		if err != nil {
			return fmt.Errorf("load proxy CA: %w (run dev-mode cert install)", err)
		}
		tlsConfig := &tls.Config{
			Certificates:   []tls.Certificate{certPair},
			GetCertificate: certProvider.getCertificate,
		}
		ln, err := tls.Listen("tcp", httpsAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("proxy https listen: %w", err)
		}
		httpsSrv := &http.Server{
			Handler:           http.HandlerFunc(s.handleProxyHTTPS),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			_ = httpsSrv.Serve(ln)
		}()
	}

	if httpAddr != "" {
		ln, err := net.Listen("tcp", httpAddr)
		if err != nil {
			return fmt.Errorf("proxy http listen: %w", err)
		}
		httpSrv := &http.Server{
			Handler:           http.HandlerFunc(s.handleProxyHTTP),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			_ = httpSrv.Serve(ln)
		}()
	}

	if err := s.startTCPProxyListeners(); err != nil {
		return err
	}

	return nil
}

func (s *Server) startTCPProxyListeners() error {
	if s.config == nil {
		return nil
	}
	routes := processProxyMatchers(s.config)
	for _, route := range routes {
		if route.TCPListen <= 0 {
			continue
		}
		if !route.Singleton {
			logError(http.StatusBadRequest, "tcp_proxy_requires_singleton", fmt.Errorf("process %s tcp_listen requires singleton=true", route.Process))
			continue
		}
		addr := fmt.Sprintf("127.0.0.1:%d", route.TCPListen)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("tcp proxy listen %s for %s: %w", addr, route.Process, err)
		}
		go s.serveTCPProxyListener(ln, route.Process)
	}
	return nil
}

func (s *Server) serveTCPProxyListener(ln net.Listener, process string) {
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handleTCPProxyConn(conn, process)
	}
}

func (s *Server) handleTCPProxyConn(conn net.Conn, process string) {
	defer conn.Close()
	mainSlug, err := resolveMainWorktreeSlug(s.mainPath)
	if err != nil {
		logError(http.StatusBadGateway, "tcp_proxy_main_slug_error", err)
		return
	}
	network, address, err := s.manager.EnsureProcessForTarget(mainSlug, process)
	if err != nil {
		logError(http.StatusBadGateway, "tcp_proxy_target_error", err)
		return
	}
	upstream, err := net.Dial(network, address)
	if err != nil {
		logError(http.StatusBadGateway, "tcp_proxy_dial_error", err)
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, conn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, upstream)
		done <- struct{}{}
	}()
	<-done
}

func proxyListenAddrs(daemonCfg *config.DaemonConfig) (httpAddr, httpsAddr string) {
	httpAddr, httpDisabled := listenFromEnv("DEV_MODE_PROXY_LISTEN_HTTP")
	httpsAddr, httpsDisabled := listenFromEnv("DEV_MODE_PROXY_LISTEN_HTTPS")

	if httpAddr == "" && !httpDisabled && daemonCfg != nil && daemonCfg.LocalProxy.ListenHTTP != "" {
		httpAddr = daemonCfg.LocalProxy.ListenHTTP
	}
	if httpsAddr == "" && !httpsDisabled && daemonCfg != nil && daemonCfg.LocalProxy.ListenHTTPS != "" {
		httpsAddr = daemonCfg.LocalProxy.ListenHTTPS
	}
	if httpAddr == "" && !httpDisabled {
		httpAddr = defaultProxyHTTPListen()
	}
	if httpsAddr == "" && !httpsDisabled {
		httpsAddr = defaultProxyHTTPSListen()
	}
	return httpAddr, httpsAddr
}

func listenFromEnv(name string) (string, bool) {
	env := strings.TrimSpace(os.Getenv(name))
	if env == "" {
		return "", false
	}
	if env == "off" || env == "disabled" {
		return "", true
	}
	return env, false
}

func (s *Server) handleProxyHTTP(w http.ResponseWriter, r *http.Request) {
	if err := s.checkProxyAllow(r); err != nil {
		writeErrorWithCode(w, http.StatusForbidden, "proxy_denied", err)
		return
	}
	host := r.Host
	if host == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_host", errors.New("missing host"))
		return
	}
	targetHost := host
	if strings.Contains(targetHost, ":") {
		targetHost, _, _ = strings.Cut(targetHost, ":")
	}
	_, httpsAddr := proxyListenAddrs(s.daemonConfig)
	if httpsAddr != "" {
		if _, port, err := net.SplitHostPort(httpsAddr); err == nil && port != "443" {
			targetHost = net.JoinHostPort(targetHost, port)
		}
	}
	target := fmt.Sprintf("https://%s%s", targetHost, r.URL.RequestURI())
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func (s *Server) handleProxyHTTPS(w http.ResponseWriter, r *http.Request) {
	if err := s.checkProxyAllow(r); err != nil {
		writeErrorWithCode(w, http.StatusForbidden, "proxy_denied", err)
		return
	}
	host := r.Host
	if host == "" {
		writeErrorWithCode(w, http.StatusBadRequest, "missing_host", errors.New("missing host"))
		return
	}
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, ":")
	}
	routeHost := host
	isGatewayTunnel := false
	if mappedHost, ok := s.localProxyHostForTunnelRequest(host); ok {
		routeHost = mappedHost
		isGatewayTunnel = true
	}

	network, address, process, gatewayMode, gatewayDebugLog, targetSlug, err := s.resolveProxyTargetForRequest(routeHost, r.URL.Path, isGatewayTunnel)
	if err != nil {
		if errors.Is(err, errGatewayProcessNotExposed) {
			writeErrorWithCode(w, http.StatusForbidden, "proxy_gateway_not_exposed", err)
			return
		}
		writeErrorWithCode(w, http.StatusBadGateway, "proxy_target_error", err)
		return
	}
	rewriteMode := isGatewayTunnel && gatewayMode == config.GatewayModeRewrite
	transcriptRelPath := ""
	if isGatewayTunnel {
		transcriptRelPath = strings.TrimSpace(gatewayDebugLog)
	}
	debugGatewayTranscript := transcriptRelPath != ""
	transcriptWorktreePath := ""
	if debugGatewayTranscript {
		if path, ok := s.manager.WorktreePath(targetSlug); ok {
			transcriptWorktreePath = path
		}
	}
	var requestForTranscript *http.Request
	var requestBodyForTranscript []byte
	if debugGatewayTranscript {
		body, err := snapshotRequestBody(r)
		if err == nil {
			requestForTranscript = r.Clone(r.Context())
			requestForTranscript.Header = r.Header.Clone()
			requestBodyForTranscript = body
		}
	}

	target, _ := url.Parse("http://unix")
	reverseProxy := httputil.NewSingleHostReverseProxy(target)
	localApex := s.projectApexZone()
	publicApex, hasPublicApex := derivePublicApex(routeHost, host, localApex)
	originalDirector := reverseProxy.Director
	reverseProxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = routeHost
		applyForwardedHeaders(req, rewriteMode)
		if rewriteMode && hasPublicApex {
			rewriteRequestCookieDomain(req.Header, publicApex, localApex)
			rewriteRequestOrigin(req.Header, publicApex, localApex)
		}
	}
	reverseProxy.Transport = &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial(network, address)
		},
	}
	rewriteDomain, rewroteDomain := replaceDomainApex(routeHost, localApex, publicApex)
	if !rewroteDomain || rewriteDomain == "" {
		rewriteDomain = host
	}
	reverseProxy.ModifyResponse = func(resp *http.Response) error {
		if rewriteMode {
			if loc := resp.Header.Get("Location"); loc != "" {
				if rewritten, ok := rewriteLocation(loc, localApex, publicApex); ok {
					resp.Header.Set("Location", rewritten)
				}
			}
			if localApex != "" && hasPublicApex {
				rewriteSetCookieDomain(resp.Header, localApex, publicApex)
			}
			if err := rewriteResponseBody(resp, routeHost, rewriteDomain); err != nil {
				return err
			}
		}
		if debugGatewayTranscript {
			respBody, err := snapshotResponseBody(resp)
			if err != nil {
				return nil
			}
			if requestForTranscript != nil && transcriptWorktreePath != "" {
				if err := writeGatewayHTTPTranscript(transcriptWorktreePath, transcriptRelPath, requestForTranscript, requestBodyForTranscript, resp, respBody); err != nil {
					writeDaemonLogLine(fmt.Sprintf("gateway transcript write failed process=%s host=%s path=%s err=%q", process, host, transcriptRelPath, err.Error()))
				}
			}
		}
		return nil
	}
	reverseProxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
		writeErrorWithCode(rw, http.StatusBadGateway, "proxy_upstream_error", err)
	}
	reverseProxy.ServeHTTP(w, r)
}

func snapshotRequestBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if closeErr := req.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return body, nil
}

func snapshotResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	return body, nil
}

func applyForwardedHeaders(req *http.Request, rewriteMode bool) {
	req.Header.Set("X-Forwarded-Proto", "https")
	if !rewriteMode {
		return
	}
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-For")
}

func proxyCertPaths() (leafCert, leafKey, caKey, caCert string, err error) {
	dir, err := config.ExpandUserPath("~/.config/dev-mode/certs")
	if err != nil {
		return "", "", "", "", err
	}
	return filepath.Join(dir, "localhost.pem"),
		filepath.Join(dir, "localhost-key.pem"),
		filepath.Join(dir, "ca-key.pem"),
		filepath.Join(dir, "ca.pem"),
		nil
}

func (s *Server) checkProxyAllow(r *http.Request) error {
	allow := ""
	if s.daemonConfig != nil {
		allow = strings.TrimSpace(s.daemonConfig.LocalProxy.Allow)
	}
	if allow == "" {
		allow = "loopback"
	}
	if allow == "all" {
		return nil
	}
	if allow != "loopback" {
		return fmt.Errorf("invalid proxy.allow: %s", allow)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return errors.New("invalid remote address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("proxy access denied")
	}
	return nil
}
