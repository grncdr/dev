package agent

import (
	"bufio"
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
	"dev/internal/utils"
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
	// RewritePeerSubdomains controls rewrite-mode translation of peer local
	// hostnames (for example "minio.main.localhost" -> "minio.<label>.<zone>").
	RewritePeerSubdomains []string
	// ResolvedLocalHost is the canonical local hostname for the resolved target
	// process. HandleTunnelRequest uses this for upstream Host and rewrite logic.
	ResolvedLocalHost string
	// DefaultSubdomain is the worktree's fallback subdomain, populated even
	// when ResolveRouting returns an error. When set and the request hits the
	// base public host with no matcher, HandleTunnelRequest redirects to
	// <DefaultSubdomain>.<public host>.
	DefaultSubdomain string
	// NoAuth reports that the resolved service opts out of share auth
	// (`[gateway.expose.<service>] auth = false`). When true, the request is
	// served without Basic Auth even if the tunnel carries credentials.
	NoAuth bool
	// DefaultSubdomainNoAuth reports that the worktree's default-subdomain
	// service opts out of share auth. Used only on the base-host redirect path
	// to decide whether the redirect itself requires auth.
	DefaultSubdomainNoAuth bool
	// WebSocketPaths lists request path prefixes whose WebSocket upgrade
	// requests are exempted from share auth. Browsers cannot attach Basic Auth
	// to a WebSocket handshake; this is the opt-in escape hatch. Match is
	// segment-boundary prefix and applies only to upgrade requests.
	WebSocketPaths []string
}

// TunnelProxyOptions holds the callbacks needed by HandleTunnelRequest to
// resolve targets, manage proxy sessions, and log.
type TunnelProxyOptions struct {
	// ResolveRouting maps a local hostname and request path to a target service,
	// returning routing metadata (including the auth opt-out) WITHOUT starting
	// the process. On a no-match error it still populates DefaultSubdomain and
	// DefaultSubdomainNoAuth so the caller can issue a redirect.
	ResolveRouting func(host, path string) (TunnelResolveResult, error)
	// EnsureTarget starts (if needed) the resolved service's process and returns
	// its dialable target plus the canonical local host. It runs only after the
	// auth decision, so an unauthenticated request never starts a process.
	EnsureTarget func(host string, resolved TunnelResolveResult) (target ProxyTarget, resolvedLocalHost string, err error)
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
	if opts.ResolveRouting == nil || opts.EnsureTarget == nil || opts.EndProxySession == nil || opts.ProjectApexZone == nil {
		return errors.New("incomplete tunnel proxy options")
	}
	publicHost := normalizeProxyHost(req.Host)
	localHost := localHostForTunnelRequest(publicHost, tunnel)
	if localHost == "" {
		return fmt.Errorf("could not resolve local host for tunnel request host %q", req.Host)
	}

	authenticated := func() bool {
		return authenticateGatewayTunnelCredentials(req, tunnel.AuthUsername, tunnel.AuthPassword)
	}

	logf := func(format string, args ...any) {
		if opts.Logf != nil {
			opts.Logf(format, args...)
		}
	}

	isUpgrade := isUpgradeHTTPRequest(req)
	if isUpgrade {
		logf("tunnel upgrade request host=%s path=%s upgrade=%s", publicHost, req.URL.Path, req.Header.Get("Upgrade"))
	}

	authRequired := func(reason string) error {
		logf("tunnel auth required host=%s path=%s upgrade=%t reason=%s", publicHost, req.URL.Path, isUpgrade, reason)
		return writeTunnelAuthRequired(stream)
	}

	resolved, err := opts.ResolveRouting(localHost, req.URL.Path)
	if err != nil {
		// Default-secure: the redirect (and any error) still requires auth unless
		// the target the request would reach is an explicitly public service.
		if location, ok := defaultSubdomainRedirectForTunnel(publicHost, tunnel.Label, resolved.DefaultSubdomain, req); ok {
			if !resolved.DefaultSubdomainNoAuth && !authenticated() {
				return authRequired("redirect-protected-default")
			}
			return writeTunnelRedirect(stream, location)
		}
		if !authenticated() {
			return authRequired("unmatched-host")
		}
		return err
	}

	authExempt := resolved.NoAuth || (isUpgrade && pathMatchesWebSocketExempt(req.URL.Path, resolved.WebSocketPaths))
	if isUpgrade {
		logf("tunnel upgrade resolved host=%s path=%s process=%s websocket_paths=%v auth_exempt=%t no_auth=%t",
			publicHost, req.URL.Path, resolved.Process, resolved.WebSocketPaths, authExempt, resolved.NoAuth)
	}
	if !authExempt && !authenticated() {
		return authRequired("service-protected")
	}

	target, resolvedLocalHost, err := opts.EnsureTarget(localHost, resolved)
	if err != nil {
		return err
	}
	resolved.ProxyTarget = target
	resolved.ResolvedLocalHost = resolvedLocalHost
	defer opts.EndProxySession(resolved.Slug, resolved.Path, resolved.Process)

	if isUpgrade {
		return forwardTunnelUpgrade(ctx, req, resolved.ProxyTarget, resolvedLocalHost, localHost, stream)
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
	resolvedLocalHost = normalizeProxyHost(resolvedLocalHost)
	if resolvedLocalHost == "" {
		resolvedLocalHost = localHost
	}
	outReq.Host = resolvedLocalHost
	if reqBody != nil {
		outReq.Body = io.NopCloser(bytes.NewReader(reqBody))
		outReq.ContentLength = int64(len(reqBody))
		outReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(reqBody)), nil
		}
	}

	rewriteMode := resolved.GatewayMode == config.GatewayModeRewrite
	localApex := opts.ProjectApexZone()
	publicApex, hasPublicApex := DerivePublicApex(resolvedLocalHost, publicHost, localApex)
	applyForwardedHeaders(outReq, rewriteMode)
	if rewriteMode && hasPublicApex {
		RewriteRequestCookieDomainForTunnel(outReq.Header, publicHost, resolvedLocalHost, publicApex, localApex)
		RewriteRequestOriginForTunnel(outReq.Header, publicHost, resolvedLocalHost, publicApex, localApex)
		RewriteRequestRefererForTunnel(outReq.Header, publicHost, resolvedLocalHost, publicApex, localApex)
	}
	if resolved.GatewayMode != "" {
		outReq.Header.Set("Dev-Gateway-Mode", resolved.GatewayMode)
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

	rewriteDomain, rewroteDomain := ReplaceHostLocalToPublic(resolvedLocalHost, resolvedLocalHost, publicHost, localApex, publicApex)
	if !rewroteDomain || rewriteDomain == "" {
		rewriteDomain = publicHost
	}
	if rewriteMode {
		if loc := resp.Header.Get("Location"); loc != "" {
			if rewritten, ok := RewriteLocationForTunnelWithPeerSubdomains(loc, resolvedLocalHost, publicHost, localApex, publicApex, resolved.RewritePeerSubdomains); ok {
				resp.Header.Set("Location", rewritten)
			}
		}
		if localApex != "" && hasPublicApex {
			RewriteSetCookieDomainForTunnelWithPeerSubdomains(resp.Header, resolvedLocalHost, publicHost, localApex, publicApex, resolved.RewritePeerSubdomains)
		}
		if err := RewriteResponseBodyForTunnel(resp, resolvedLocalHost, rewriteDomain, localApex, publicApex, resolved.RewritePeerSubdomains); err != nil {
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

func defaultSubdomainRedirectForTunnel(publicHost, label, defaultSubdomain string, req *http.Request) (string, bool) {
	defaultSubdomain = strings.ToLower(strings.TrimSpace(defaultSubdomain))
	label = strings.ToLower(strings.TrimSpace(label))
	host := normalizeProxyHost(publicHost)
	if defaultSubdomain == "" || label == "" || host == "" {
		return "", false
	}
	parts := strings.Split(host, ".")
	if len(parts) == 0 || parts[0] != label {
		return "", false
	}
	target := &url.URL{
		Scheme: "https",
		Host:   defaultSubdomain + "." + host,
	}
	if req != nil && req.URL != nil {
		target.Path = req.URL.Path
		target.RawPath = req.URL.RawPath
		target.RawQuery = req.URL.RawQuery
	}
	return target.String(), true
}

func writeTunnelRedirect(stream net.Conn, location string) error {
	resp := &http.Response{
		StatusCode: http.StatusFound,
		Status:     fmt.Sprintf("%d %s", http.StatusFound, http.StatusText(http.StatusFound)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
	resp.Header.Set("Location", location)
	resp.Header.Set("Content-Length", "0")
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

// pathMatchesWebSocketExempt reports whether reqPath falls under one of the
// configured WebSocket exempt prefixes, using segment-boundary prefix match.
// "/cable" matches "/cable" and "/cable/x" but not "/cablecar".
func pathMatchesWebSocketExempt(reqPath string, exempt []string) bool {
	if reqPath == "" {
		reqPath = "/"
	}
	for _, prefix := range exempt {
		if prefix == "" {
			continue
		}
		if reqPath == prefix {
			return true
		}
		if strings.HasPrefix(reqPath, prefix+"/") {
			return true
		}
	}
	return false
}

// forwardTunnelUpgrade dials the resolved process target and proxies an
// HTTP upgrade (e.g. WebSocket) handshake bidirectionally between the gateway
// stream and the upstream. Rewrite-mode header/body translation does not
// apply: the handshake has no rewriteable body, and 101 carries no Location
// or Set-Cookie. We pass headers through with the Host set to the resolved
// local host and X-Forwarded-Proto for parity with the regular HTTP path.
func forwardTunnelUpgrade(ctx context.Context, req *http.Request, target ProxyTarget, resolvedLocalHost, localHost string, stream net.Conn) error {
	if target.Network == "" || target.Address == "" {
		return errors.New("upgrade forwarding requires resolved upstream target")
	}
	upstream, err := net.Dial(target.Network, target.Address)
	if err != nil {
		return err
	}
	defer upstream.Close()

	host := normalizeProxyHost(resolvedLocalHost)
	if host == "" {
		host = localHost
	}

	outReq := req.Clone(ctx)
	outReq.Header = req.Header.Clone()
	if outReq.URL == nil {
		outReq.URL = &url.URL{Path: "/"}
	}
	outReq.URL.Scheme = "http"
	outReq.URL.Host = "dev-tunnel-upstream"
	outReq.RequestURI = ""
	outReq.Host = host
	outReq.Header.Set("X-Forwarded-Proto", "https")

	if err := outReq.Write(upstream); err != nil {
		return err
	}
	upstreamReader := bufio.NewReader(upstream)
	resp, err := http.ReadResponse(upstreamReader, outReq)
	if err != nil {
		return err
	}
	if err := resp.Write(stream); err != nil {
		_ = resp.Body.Close()
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		_, err := io.Copy(stream, resp.Body)
		return err
	}
	_ = resp.Body.Close()
	return utils.ProxyBidirectional(stream, nil, upstream, upstreamReader)
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
