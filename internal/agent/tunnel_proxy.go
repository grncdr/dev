package agent

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"dev/internal/config"
)

// TunnelResolveResult extends ProxyTarget with gateway-specific metadata
// needed to proxy a tunnel request to a local process.
type TunnelResolveResult struct {
	ProxyTarget
	// GatewayMode is the expose mode from config.GatewayExposeRule.Mode
	// ("reverse_proxy" or "rewrite"). Controls whether response headers and
	// body are rewritten to translate between public and local hostnames.
	GatewayMode string
	// GatewayDebugLog is the relative path for HTTP transcript logging, if configured.
	GatewayDebugLog string
}

// TunnelProxyOptions holds the callbacks needed by HandleTunnelRequest to
// resolve targets, manage proxy sessions, and log.
type TunnelProxyOptions struct {
	// ResolveTarget maps a local hostname and request path to an upstream process.
	ResolveTarget func(host, path string) (TunnelResolveResult, error)
	// EndProxySession is called when the proxied request completes, allowing
	// the daemon to decrement active proxy session counts.
	EndProxySession func(targetSlug, targetPath, process string)
	// ProjectApexZone returns the local DNS apex zone (e.g. ".localhost").
	ProjectApexZone func() string
	// WorktreePath resolves a slug+path hint to the worktree's filesystem path.
	WorktreePath func(targetSlug, targetPath string) (string, bool)
	// Logf is an optional structured logger for diagnostics.
	Logf func(format string, args ...any)
}

func HandleTunnelRequest(ctx context.Context, opts TunnelProxyOptions, tunnel TunnelStatus, req *http.Request, stream net.Conn) error {
	if req == nil {
		return errors.New("missing request")
	}
	if opts.ResolveTarget == nil || opts.EndProxySession == nil || opts.ProjectApexZone == nil {
		return errors.New("incomplete tunnel proxy options")
	}
	if !authenticateGatewayTunnelCredentials(req, tunnel.AuthUsername, tunnel.AuthPassword) {
		return writeTunnelAuthRequired(stream)
	}
	publicHost := normalizeProxyHost(req.Host)
	localHost := localHostForTunnelRequest(publicHost, tunnel)
	if localHost == "" {
		return fmt.Errorf("could not resolve local host for tunnel request host %q", req.Host)
	}

	resolved, err := opts.ResolveTarget(localHost, req.URL.Path)
	if err != nil {
		return err
	}
	defer opts.EndProxySession(resolved.Slug, resolved.Path, resolved.Process)

	if isUpgradeHTTPRequest(req) {
		return errors.New("upgrade requests are not yet supported for direct tunnel routing")
	}

	reqBody, err := snapshotRequestBody(req)
	if err != nil {
		return err
	}
	outReq := req.Clone(ctx)
	outReq.Header = req.Header.Clone()
	if outReq.URL == nil {
		outReq.URL = &url.URL{Path: "/"}
	}
	outReq.URL.Scheme = "http"
	outReq.URL.Host = "dev-tunnel-upstream"
	outReq.RequestURI = ""
	outReq.Host = localHost
	if reqBody != nil {
		outReq.Body = io.NopCloser(bytes.NewReader(reqBody))
		outReq.ContentLength = int64(len(reqBody))
		outReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(reqBody)), nil
		}
	}

	rewriteMode := resolved.GatewayMode == config.GatewayModeRewrite
	localApex := opts.ProjectApexZone()
	publicApex, hasPublicApex := DerivePublicApex(localHost, publicHost, localApex)
	applyForwardedHeaders(outReq, rewriteMode)
	if rewriteMode && hasPublicApex {
		RewriteRequestCookieDomainForTunnel(outReq.Header, publicHost, localHost, publicApex, localApex)
		RewriteRequestOriginForTunnel(outReq.Header, publicHost, localHost, publicApex, localApex)
	}

	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return net.Dial(resolved.Network, resolved.Address)
		},
	}
	resp, err := transport.RoundTrip(outReq.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	defer transport.CloseIdleConnections()

	rewriteDomain, rewroteDomain := ReplaceHostLocalToPublic(localHost, localHost, publicHost, localApex, publicApex)
	if !rewroteDomain || rewriteDomain == "" {
		rewriteDomain = publicHost
	}
	if rewriteMode {
		if loc := resp.Header.Get("Location"); loc != "" {
			if rewritten, ok := RewriteLocationForTunnel(loc, localHost, publicHost, localApex, publicApex); ok {
				resp.Header.Set("Location", rewritten)
			}
		}
		if localApex != "" && hasPublicApex {
			RewriteSetCookieDomainForTunnel(resp.Header, localHost, publicHost, localApex, publicApex)
		}
		if err := RewriteResponseBody(resp, localHost, rewriteDomain); err != nil {
			return err
		}
	}

	transcriptRelPath := strings.TrimSpace(resolved.GatewayDebugLog)
	if transcriptRelPath != "" && opts.WorktreePath != nil {
		if respBody, err := snapshotResponseBody(resp); err == nil {
			if worktreePath, ok := opts.WorktreePath(resolved.Slug, resolved.Path); ok && worktreePath != "" {
				reqCopy := req.Clone(req.Context())
				reqCopy.Header = req.Header.Clone()
				if err := WriteGatewayHTTPTranscript(worktreePath, transcriptRelPath, reqCopy, reqBody, resp, respBody); err != nil && opts.Logf != nil {
					opts.Logf("gateway transcript write failed process=%s host=%s path=%s err=%q", resolved.Process, publicHost, transcriptRelPath, err.Error())
				}
			}
		}
	}
	return resp.Write(stream)
}

func authenticateGatewayTunnelCredentials(req *http.Request, username, password string) bool {
	username = strings.TrimSpace(username)
	if username == "" && password == "" {
		return true
	}
	u, p, ok := req.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(u), []byte(username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
}

func writeTunnelAuthRequired(stream net.Conn) error {
	const msg = "invalid share credentials"
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     fmt.Sprintf("%d %s", http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(msg)),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(msg)))
	resp.Header.Set("WWW-Authenticate", `Basic realm="dev share"`)
	return resp.Write(stream)
}

func localHostForTunnelRequest(publicHost string, tunnel TunnelStatus) string {
	localBaseHost := normalizeProxyHost(tunnel.LocalBaseHost)
	if localBaseHost == "" {
		return ""
	}
	label := strings.ToLower(strings.TrimSpace(tunnel.Label))
	if label == "" {
		return localBaseHost
	}
	hostLabels := strings.Split(normalizeProxyHost(publicHost), ".")
	baseLabels := strings.Split(localBaseHost, ".")
	if len(hostLabels) == 0 || len(baseLabels) == 0 {
		return localBaseHost
	}
	for i := len(hostLabels) - 1; i >= 0; i-- {
		if hostLabels[i] != label {
			continue
		}
		prefix := hostLabels[:i]
		return strings.Join(append(prefix, baseLabels...), ".")
	}
	return localBaseHost
}

func isUpgradeHTTPRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	connection := strings.ToLower(req.Header.Get("Connection"))
	upgrade := strings.ToLower(req.Header.Get("Upgrade"))
	return strings.Contains(connection, "upgrade") && upgrade != ""
}

func applyForwardedHeaders(req *http.Request, rewriteMode bool) {
	req.Header.Set("X-Forwarded-Proto", "https")
	if !rewriteMode {
		return
	}
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-For")
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
