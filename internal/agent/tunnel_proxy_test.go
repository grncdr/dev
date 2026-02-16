package agent

import (
	"bufio"
	"context"
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
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
		ResolveTarget: func(host, path string) (TunnelResolveResult, error) {
			resolvedHost = host
			return TunnelResolveResult{
				ProxyTarget: ProxyTarget{
					Network: "tcp",
					Address: upstreamURL.Host,
					Slug:    "main",
					Path:    "/repo/main",
					Process: "minio",
				},
				GatewayMode:           config.GatewayModeRewrite,
				RewritePeerSubdomains: []string{"minio"},
				GatewayDebugLog:       "",
				ResolvedLocalHost:     "minio.main.localhost",
			}, nil
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
}
