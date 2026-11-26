package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dev/internal/config"
	"dev/internal/worktree"
)

type webSocketEchoServer struct {
	Addr string
}

func startWebSocketEchoServer(t *testing.T) webSocketEchoServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			_ = rw.Flush()
		}),
		ReadHeaderTimeout: 2 * time.Second,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	go func() { _ = srv.Serve(ln) }()
	return webSocketEchoServer{Addr: ln.Addr().String()}
}

func newWebSocketUpgradeRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	u, err := url.Parse(target)
	if err == nil {
		req.Host = u.Host
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "testtesttest=")
	return req
}

func runTunnelUpgrade(t *testing.T, opts TunnelProxyOptions, tunnel TunnelStatus, req *http.Request) (*http.Response, *bufio.Reader) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()
	br := bufio.NewReader(clientSide)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	// Drain errCh in the background so the handler goroutine can finish; some
	// upgrade flows keep proxying after the 101 until the client closes.
	go func() { <-errCh }()
	return resp, br
}

func TestHandleTunnelRequest_RewriteMode_RewritesSingletonPeerHostToMainLabel(t *testing.T) {
	t.Parallel()

	var upstreamHost string
	var gatewayMode string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		gatewayMode = r.Header.Get("Dev-Gateway-Mode")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	var resolvedHost string
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			resolvedHost = host
			return TunnelResolveResult{
				ProxyTarget:           ProxyTarget{Slug: "main", Path: "/repo/main", Process: "minio"},
				GatewayMode:           config.GatewayModeRewrite,
				RewritePeerSubdomains: []string{"minio"},
				GatewayDebugLog:       "",
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{
				Network: "tcp",
				Address: upstreamURL.Host,
				Slug:    "main",
				Path:    "/repo/main",
				Process: "minio",
			}, "minio.main.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://minio.my-feature.wip.example.com/bucket/object?X-Amz-SignedHeaders=host", nil)
	req.Host = "minio.my-feature.wip.example.com"

	tunnel := TunnelStatus{
		Identifier:    worktree.Identifier{Slug: "my-feature"},
		Label:         "my-feature",
		LocalBaseHost: "my-feature.localhost",
	}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()

	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read tunneled response: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read tunneled body: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if resolvedHost != "minio.my-feature.localhost" {
		t.Fatalf("expected resolved host minio.my-feature.localhost, got %q", resolvedHost)
	}
	if upstreamHost != "minio.main.localhost" {
		t.Fatalf("expected upstream host minio.main.localhost, got %q", upstreamHost)
	}
	if gatewayMode != config.GatewayModeRewrite {
		t.Fatalf("expected Dev-Gateway-Mode=%q, got %q", config.GatewayModeRewrite, gatewayMode)
	}
}

func TestHandleTunnelRequest_ReverseProxyMode_SetsGatewayModeAndForwardedHostHeaders(t *testing.T) {
	t.Parallel()

	var upstreamHost string
	var gatewayMode string
	var forwardedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		gatewayMode = r.Header.Get("Dev-Gateway-Mode")
		forwardedHost = r.Header.Get("X-Forwarded-Host")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:     ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "app"},
				GatewayMode:     config.GatewayModeReverseProxy,
				GatewayDebugLog: "",
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{
				Network: "tcp",
				Address: upstreamURL.Host,
				Slug:    "feature",
				Path:    "/repo/feature",
				Process: "app",
			}, "app.feature.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	// The gateway forwards the public Host and adds no X-Forwarded-* headers.
	req := httptest.NewRequest(http.MethodGet, "https://app.feature.public.example.com/", nil)
	req.Host = "app.feature.public.example.com"

	tunnel := TunnelStatus{
		Identifier:    worktree.Identifier{Slug: "feature"},
		Label:         "feature",
		LocalBaseHost: "feature.localhost",
	}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()

	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read tunneled response: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read tunneled body: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if upstreamHost != "app.feature.localhost" {
		t.Fatalf("expected upstream host app.feature.localhost, got %q", upstreamHost)
	}
	if gatewayMode != config.GatewayModeReverseProxy {
		t.Fatalf("expected Dev-Gateway-Mode=%q, got %q", config.GatewayModeReverseProxy, gatewayMode)
	}
	if forwardedHost != "app.feature.public.example.com" {
		t.Fatalf("expected X-Forwarded-Host app.feature.public.example.com, got %q", forwardedHost)
	}
}

func TestHandleTunnelRequest_RedirectsBaseHostToDefaultSubdomain(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{DefaultSubdomain: "app"}, errors.New("no proxy matcher matched")
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run when redirecting")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {
			t.Fatalf("EndProxySession must not run when redirecting; got slug=%q path=%q process=%q", targetSlug, targetPath, process)
		},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://my-feature.public.example.com/foo?bar=1", nil)
	req.Host = "my-feature.public.example.com"

	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "my-feature"}, Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()

	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected status 302, got %d", resp.StatusCode)
	}
	want := "https://app.my-feature.public.example.com/foo?bar=1"
	if got := resp.Header.Get("Location"); got != want {
		t.Fatalf("expected Location %q, got %q", want, got)
	}
}

func TestHandleTunnelRequest_ServesOptedOutServiceWithoutCredentials(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "webhooks"},
				GatewayMode: config.GatewayModeReverseProxy,
				NoAuth:      true,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstreamURL.Host, Slug: "feature", Path: "/repo/feature", Process: "webhooks"}, "webhooks.feature.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://webhooks.feature.public.example.com/", nil)
	req.Host = "webhooks.feature.public.example.com"
	// No Authorization header, but the tunnel carries credentials.
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 for opted-out service, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_ProtectedSiblingRequiresAuthAndDoesNotStartProcess(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "admin"},
				GatewayMode: config.GatewayModeReverseProxy,
				NoAuth:      false,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run for an unauthenticated request to a protected service")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://admin.feature.public.example.com/", nil)
	req.Host = "admin.feature.public.example.com"
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for protected sibling, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_BaseHostRedirectRequiresAuthWhenDefaultProtected(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{DefaultSubdomain: "app", DefaultSubdomainNoAuth: false}, errors.New("no proxy matcher matched")
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://feature.public.example.com/", nil)
	req.Host = "feature.public.example.com"
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected status 401 for redirect to protected default, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_BaseHostRedirectSkipsAuthWhenDefaultPublic(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{DefaultSubdomain: "app", DefaultSubdomainNoAuth: true}, errors.New("no proxy matcher matched")
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://feature.public.example.com/foo?bar=1", nil)
	req.Host = "feature.public.example.com"
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected status 302 for public default redirect, got %d", resp.StatusCode)
	}
	want := "https://app.feature.public.example.com/foo?bar=1"
	if got := resp.Header.Get("Location"); got != want {
		t.Fatalf("expected Location %q, got %q", want, got)
	}
}

func TestHandleTunnelRequest_WebSocketUpgradeOnExemptPathSkipsAuth(t *testing.T) {
	t.Parallel()

	upstream := startWebSocketEchoServer(t)
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:    ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode:    config.GatewayModeReverseProxy,
				WebSocketPaths: []string{"/cable"},
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstream.Addr, Slug: "feature", Path: "/repo/feature", Process: "rails"}, "rails.feature.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.feature.public.example.com/cable/v1")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 101 for exempt WS path, got %d body=%q", resp.StatusCode, string(body))
	}
}

func TestHandleTunnelRequest_WebSocketUpgradeOnNonExemptPathRequiresAuth(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:    ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode:    config.GatewayModeReverseProxy,
				WebSocketPaths: []string{"/cable"},
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run for unauthenticated WS on non-exempt path")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.feature.public.example.com/admin")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for WS on non-exempt path, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_LogsUpgradeAndAuthDecisions(t *testing.T) {
	t.Parallel()

	var logs []string
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:    ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode:    config.GatewayModeReverseProxy,
				WebSocketPaths: []string{"/cable"},
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run for unauthenticated WS on non-exempt path")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}

	req := newWebSocketUpgradeRequest("https://rails.feature.public.example.com/admin")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	var sawUpgrade, sawAuth bool
	for _, line := range logs {
		if strings.Contains(line, "upgrade") && strings.Contains(line, "/admin") {
			sawUpgrade = true
		}
		if strings.Contains(line, "auth required") && strings.Contains(line, "/admin") {
			sawAuth = true
		}
	}
	if !sawUpgrade {
		t.Fatalf("expected an upgrade log line referencing /admin; got %v", logs)
	}
	if !sawAuth {
		t.Fatalf("expected an auth-required log line referencing /admin; got %v", logs)
	}
}

func TestHandleTunnelRequest_PlainHTTPOnWebSocketPathStillRequiresAuth(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:    ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode:    config.GatewayModeReverseProxy,
				WebSocketPaths: []string{"/cable"},
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run for unauthenticated plain HTTP on WS path")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://rails.feature.public.example.com/cable", nil)
	req.Host = "rails.feature.public.example.com"
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()

	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for plain HTTP on WS path, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_AuthenticatedWebSocketUpgradeWithoutExemptList(t *testing.T) {
	t.Parallel()

	upstream := startWebSocketEchoServer(t)
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode: config.GatewayModeReverseProxy,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstream.Addr, Slug: "feature", Path: "/repo/feature", Process: "rails"}, "rails.feature.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.feature.public.example.com/any/path")
	req.SetBasicAuth("alice", "secret")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 for authenticated WS upgrade with no WebSocketPaths config, got %d", resp.StatusCode)
	}
}

func TestHandleTunnelRequest_AnonymousWebSocketUpgradeOnUnmatchedHost(t *testing.T) {
	t.Parallel()

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{DefaultSubdomain: "app"}, errors.New("no proxy matcher matched")
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			t.Fatalf("EnsureTarget must not run for unmatched-host WS upgrade")
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://feature.public.example.com/anything")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous WS upgrade on unmatched host, got %d", resp.StatusCode)
	}
}

// startCapturingWebSocketServer is startWebSocketEchoServer plus a channel
// carrying the handshake request (Host and headers) the upstream received.
func startCapturingWebSocketServer(t *testing.T) (webSocketEchoServer, <-chan *http.Request) {
	t.Helper()
	handshakes := make(chan *http.Request, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured := r.Clone(context.Background())
			captured.Header = r.Header.Clone()
			select {
			case handshakes <- captured:
			default:
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			_ = rw.Flush()
		}),
		ReadHeaderTimeout: 2 * time.Second,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	go func() { _ = srv.Serve(ln) }()
	return webSocketEchoServer{Addr: ln.Addr().String()}, handshakes
}

func TestHandleTunnelRequest_RewriteMode_TranslatesUpgradeRequestHeaders(t *testing.T) {
	t.Parallel()

	upstream, handshakes := startCapturingWebSocketServer(t)
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "main", Path: "/repo/main", Process: "rails"},
				GatewayMode: config.GatewayModeRewrite,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstream.Addr, Slug: "main", Path: "/repo/main", Process: "rails"}, "rails.main.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.my-feature.wip.example.com/cable")
	req.Header.Set("Origin", "https://rails.my-feature.wip.example.com")
	req.Header.Set("Referer", "https://rails.my-feature.wip.example.com/chat?room=1")
	req.Header.Set("Cookie", "session=abc; $Domain=rails.my-feature.wip.example.com")
	req.Header.Set("X-Forwarded-Host", "rails.my-feature.wip.example.com")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "my-feature"}, Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 101, got %d body=%q", resp.StatusCode, string(body))
	}

	handshake := <-handshakes
	if handshake.Host != "rails.main.localhost" {
		t.Fatalf("expected upstream Host rails.main.localhost, got %q", handshake.Host)
	}
	if got := handshake.Header.Get("Origin"); got != "https://rails.main.localhost" {
		t.Fatalf("expected translated Origin, got %q", got)
	}
	if got := handshake.Header.Get("Referer"); got != "https://rails.main.localhost/chat?room=1" {
		t.Fatalf("expected translated Referer, got %q", got)
	}
	if got := handshake.Header.Get("Cookie"); got != "session=abc; $Domain=rails.main.localhost" {
		t.Fatalf("expected translated Cookie domain, got %q", got)
	}
	if got := handshake.Header.Get("X-Forwarded-Host"); got != "" {
		t.Fatalf("expected X-Forwarded-Host stripped in rewrite mode, got %q", got)
	}
	if got := handshake.Header.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("expected X-Forwarded-For stripped in rewrite mode, got %q", got)
	}
	if got := handshake.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("expected X-Forwarded-Proto https, got %q", got)
	}
	if got := handshake.Header.Get("Dev-Gateway-Mode"); got != config.GatewayModeRewrite {
		t.Fatalf("expected Dev-Gateway-Mode %q, got %q", config.GatewayModeRewrite, got)
	}
}

func TestHandleTunnelRequest_ReverseProxyMode_SetsForwardedHostOnUpgrade(t *testing.T) {
	t.Parallel()

	upstream, handshakes := startCapturingWebSocketServer(t)
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "main", Path: "/repo/main", Process: "rails"},
				GatewayMode: config.GatewayModeReverseProxy,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstream.Addr, Slug: "main", Path: "/repo/main", Process: "rails"}, "rails.main.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.my-feature.wip.example.com/cable")
	req.Header.Set("Origin", "https://rails.my-feature.wip.example.com")
	req.Header.Set("Referer", "https://rails.my-feature.wip.example.com/chat")
	req.Header.Set("X-Forwarded-Host", "spoofed.example.com")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "my-feature"}, Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	handshake := <-handshakes
	if got := handshake.Header.Get("Origin"); got != "https://rails.my-feature.wip.example.com" {
		t.Fatalf("expected Origin untouched in reverse-proxy mode, got %q", got)
	}
	if got := handshake.Header.Get("Referer"); got != "https://rails.my-feature.wip.example.com/chat" {
		t.Fatalf("expected Referer untouched in reverse-proxy mode, got %q", got)
	}
	if got := handshake.Header.Get("X-Forwarded-Host"); got != "rails.my-feature.wip.example.com" {
		t.Fatalf("expected X-Forwarded-Host set to the public host in reverse-proxy mode, got %q", got)
	}
	if got := handshake.Header.Get("Dev-Gateway-Mode"); got != config.GatewayModeReverseProxy {
		t.Fatalf("expected Dev-Gateway-Mode %q, got %q", config.GatewayModeReverseProxy, got)
	}
}

func TestHandleTunnelRequest_RewriteMode_TranslatesFailedUpgradeResponse(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://rails.main.localhost/login")
		w.Header().Add("Set-Cookie", "session=abc; Domain=rails.main.localhost; Path=/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}

	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{Slug: "main", Path: "/repo/main", Process: "rails"},
				GatewayMode: config.GatewayModeRewrite,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstreamURL.Host, Slug: "main", Path: "/repo/main", Process: "rails"}, "rails.main.localhost", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := newWebSocketUpgradeRequest("https://rails.my-feature.wip.example.com/cable")
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "my-feature"}, Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	resp, _ := runTunnelUpgrade(t, opts, tunnel, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from failed handshake, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "https://rails.my-feature.wip.example.com/login" {
		t.Fatalf("expected translated Location, got %q", got)
	}
	if got := resp.Header.Get("Set-Cookie"); got != "session=abc; Domain=rails.my-feature.wip.example.com; Path=/" {
		t.Fatalf("expected translated Set-Cookie domain, got %q", got)
	}
}

func TestHandleTunnelRequest_DoesNotRedirectWhenSubdomainPresent(t *testing.T) {
	t.Parallel()

	resolveErr := errors.New("no proxy matcher matched")
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{DefaultSubdomain: "app"}, resolveErr
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{}, "", nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return ".localhost" },
	}

	req := httptest.NewRequest(http.MethodGet, "https://missing.my-feature.public.example.com/", nil)
	req.Host = "missing.my-feature.public.example.com"

	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "my-feature"}, Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	err := HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected resolve error to propagate, got %v", err)
	}
}

// rewriteModeShare describes a rewrite-mode share for runRewriteModeTunnel.
type rewriteModeShare struct {
	localHost string   // local host of the requested process, e.g. "api.feature.localhost"
	localBase string   // local base host of the worktree, e.g. "feature.localhost"
	localApex string   // daemon apex zone, e.g. ".localhost"
	peers     []string // rewrite_peer_subdomains
}

// runRewriteModeTunnel sends req through HandleTunnelRequest in rewrite mode to
// upstream and returns the response the gateway receives, with its body.
func runRewriteModeTunnel(t *testing.T, share rewriteModeShare, req *http.Request, upstream http.Handler) (*http.Response, string) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	upstreamURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	opts := TunnelProxyOptions{
		ResolveRouting: func(host, path string) (TunnelResolveResult, error) {
			return TunnelResolveResult{
				ProxyTarget:           ProxyTarget{Slug: "feature", Path: "/repo/feature", Process: "rails"},
				GatewayMode:           config.GatewayModeRewrite,
				RewritePeerSubdomains: share.peers,
			}, nil
		},
		EnsureTarget: func(host string, resolved TunnelResolveResult) (ProxyTarget, string, error) {
			return ProxyTarget{Network: "tcp", Address: upstreamURL.Host, Slug: "feature", Path: "/repo/feature", Process: "rails"}, share.localHost, nil
		},
		EndProxySession: func(targetSlug, targetPath, process string) {},
		ProjectApexZone: func() string { return share.localApex },
	}
	tunnel := TunnelStatus{Identifier: worktree.Identifier{Slug: "feature"}, Label: "feature", LocalBaseHost: share.localBase}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	errCh := make(chan error, 1)
	go func() {
		defer serverSide.Close()
		errCh <- HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	}()
	resp, err := http.ReadResponse(bufio.NewReader(clientSide), req)
	if err != nil {
		t.Fatalf("read tunneled response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := <-errCh; err != nil {
		t.Fatalf("handle tunnel request: %v", err)
	}
	return resp, string(body)
}

// runRewriteModeRequest sends req through a rewrite-mode share and returns the
// request the upstream received, with its body.
func runRewriteModeRequest(t *testing.T, req *http.Request) (*http.Request, string) {
	t.Helper()
	received := make(chan *http.Request, 1)
	bodies := make(chan string, 1)
	share := rewriteModeShare{localHost: "api.feature.localhost", localBase: "feature.localhost", localApex: ".localhost"}
	runRewriteModeTunnel(t, share, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- r
		bodies <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	return <-received, <-bodies
}

// In rewrite mode the app only knows its local hosts. URLs of this share that
// the client sends in the query string or a form body (e.g. the OAuth
// "resource" parameter) must reach it with the local host. Other URLs, even
// under the public apex, are left alone.
func TestHandleTunnelRequest_RewriteMode_RewritesShareURLsInQueryAndForm(t *testing.T) {
	t.Parallel()

	const query = "resource=https%3A%2F%2Fapi.feature.public.example.com%2Fmcp" +
		"&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback" +
		"&site=https%3A%2F%2Fwww.public.example.com%2F&state=a%2Bb"
	const wantQuery = "resource=https%3A%2F%2Fapi.feature.localhost%2Fmcp" +
		"&redirect_uri=https%3A%2F%2Fclaude.ai%2Fapi%2Fmcp%2Fauth_callback" +
		"&site=https%3A%2F%2Fwww.public.example.com%2F&state=a%2Bb"

	authorize := httptest.NewRequest(http.MethodGet, "https://api.feature.public.example.com/oauth/authorize?"+query, nil)
	authorize.Host = "api.feature.public.example.com"
	got, _ := runRewriteModeRequest(t, authorize)
	if got.URL.RawQuery != wantQuery {
		t.Fatalf("query mismatch:\nwant: %s\ngot:  %s", wantQuery, got.URL.RawQuery)
	}

	token := httptest.NewRequest(http.MethodPost, "https://api.feature.public.example.com/oauth/token", strings.NewReader(query))
	token.Host = "api.feature.public.example.com"
	token.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got, body := runRewriteModeRequest(t, token)
	if body != wantQuery {
		t.Fatalf("form body mismatch:\nwant: %s\ngot:  %s", wantQuery, body)
	}
	if got.ContentLength != int64(len(wantQuery)) {
		t.Fatalf("expected Content-Length %d, got %d", len(wantQuery), got.ContentLength)
	}
}

// A rewrite-mode share with a two-label local apex and peer subdomains, as in
// `rewrite_peer_subdomains = ["s3", "secure"]`. The app builds local URLs for
// itself and its peers; the client must only see public ones.
func TestHandleTunnelRequest_RewriteMode_RewritesPeerHostsInBodyAndHeaders(t *testing.T) {
	t.Parallel()

	share := rewriteModeShare{
		localHost: "api.feature.stephen.test",
		localBase: "feature.stephen.test",
		localApex: ".stephen.test",
		peers:     []string{"s3", "secure"},
	}
	newRequest := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, "https://api.feature.dropscan.dev"+path, nil)
		req.Host = "api.feature.dropscan.dev"
		return req
	}

	t.Run("JSON body", func(t *testing.T) {
		_, body := runRewriteModeTunnel(t, share, newRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = w.Write([]byte(`{"resource":"https://api.feature.stephen.test/mcp",` +
					`"authorization_servers":["https://secure.feature.stephen.test"],` +
					`"download_url":"https://s3.feature.stephen.test/bucket/key?X-Amz-Signature=abc"}`))
			}))
		want := `{"resource":"https://api.feature.dropscan.dev/mcp",` +
			`"authorization_servers":["https://secure.feature.dropscan.dev"],` +
			`"download_url":"https://s3.feature.dropscan.dev/bucket/key?X-Amz-Signature=abc"}`
		if body != want {
			t.Fatalf("body mismatch:\nwant: %s\ngot:  %s", want, body)
		}
	})

	t.Run("WWW-Authenticate", func(t *testing.T) {
		resp, _ := runRewriteModeTunnel(t, share, newRequest(http.MethodPost, "/mcp"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://api.feature.stephen.test/.well-known/oauth-protected-resource/mcp"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
		want := `Bearer resource_metadata="https://api.feature.dropscan.dev/.well-known/oauth-protected-resource/mcp"`
		if got := resp.Header.Get("WWW-Authenticate"); got != want {
			t.Fatalf("WWW-Authenticate mismatch:\nwant: %s\ngot:  %s", want, got)
		}
	})

	t.Run("Location to peer", func(t *testing.T) {
		resp, _ := runRewriteModeTunnel(t, share, newRequest(http.MethodGet, "/login"),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://secure.feature.stephen.test/oauth/authorize?client_id=x")
				w.WriteHeader(http.StatusFound)
			}))
		want := "https://secure.feature.dropscan.dev/oauth/authorize?client_id=x"
		if got := resp.Header.Get("Location"); got != want {
			t.Fatalf("Location mismatch:\nwant: %s\ngot:  %s", want, got)
		}
	})
}
