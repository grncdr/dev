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

func TestHandleTunnelRequest_ReverseProxyMode_SetsGatewayModeHeader(t *testing.T) {
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
