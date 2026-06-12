package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"dev/internal/config"
)

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
		Slug:          "my-feature",
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
		Slug:          "feature",
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

	tunnel := TunnelStatus{Slug: "my-feature", Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

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
	tunnel := TunnelStatus{Slug: "feature", Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

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
	tunnel := TunnelStatus{Slug: "feature", Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

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
	tunnel := TunnelStatus{Slug: "feature", Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

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
	tunnel := TunnelStatus{Slug: "feature", Label: "feature", LocalBaseHost: "feature.localhost", AuthUsername: "alice", AuthPassword: "secret"}

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

	tunnel := TunnelStatus{Slug: "my-feature", Label: "my-feature", LocalBaseHost: "my-feature.localhost"}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	err := HandleTunnelRequest(context.Background(), opts, tunnel, req, serverSide)
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected resolve error to propagate, got %v", err)
	}
}
