package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dev/internal/gatewayproto"
	"dev/internal/tunnelmux"
	"dev/internal/utils"
)

// Agent maintains a persistent tunnel connection to a gateway server.
// It registers with the gateway, opens a multiplexed tunnel, and dispatches
// incoming HTTP requests to HandleStream.
type Agent struct {
	// GatewayURL is the base URL of the gateway to connect to (required).
	GatewayURL string
	// Project is the project name sent during registration.
	Project string
	// Slug is the worktree slug sent during registration.
	Slug string
	// Label is the unique tunnel label registered with the gateway (required).
	Label string
	// AgentID is a unique identifier for this agent instance.
	AgentID string
	// Name is a human-readable agent name sent during registration.
	Name string
	// RetryDelay is the pause between reconnection attempts (defaults to 500ms).
	RetryDelay time.Duration
	// HTTPClient is used for non-gateway HTTP requests.
	HTTPClient *http.Client
	// GatewayClient is used for gateway API calls (registration); if nil,
	// HTTPClient is used. Typically carries mTLS client credentials issued
	// by the gateway's CertIssuer via MTLSClientForGatewayURL.
	GatewayClient *http.Client
	// TLSConfig is used for the raw TCP tunnel connection to the gateway.
	TLSConfig *tls.Config
	// HandleStream is called for each inbound HTTP request on the tunnel (required).
	HandleStream func(context.Context, *http.Request, net.Conn) error
	// OnConnected is called when the tunnel session is established.
	OnConnected func()
	// OnDisconnected is called when the tunnel session drops.
	OnDisconnected func(error)
	// OnRegistered is called after successful gateway registration.
	OnRegistered func(publicHost string)
	// OnRegisterProgress is called with incremental registration status updates.
	OnRegisterProgress func(stage, message string)
}

const (
	registerRequestTimeout = 5 * time.Minute
	registerProgressMIME   = "application/x-ndjson"
)

func (a *Agent) Run(ctx context.Context) error {
	if a.GatewayURL == "" || a.Label == "" {
		return errors.New("gateway_url and label are required")
	}
	if a.HandleStream == nil {
		return errors.New("handle_stream is required")
	}
	if a.RetryDelay <= 0 {
		a.RetryDelay = 500 * time.Millisecond
	}
	for {
		err := a.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.RetryDelay):
		}
	}
}

func (a *Agent) runOnce(ctx context.Context) (runErr error) {
	defer func() {
		if a.OnDisconnected != nil {
			a.OnDisconnected(runErr)
		}
	}()
	if err := a.register(ctx); err != nil {
		return err
	}
	conn, err := a.connectTunnel(ctx)
	if err != nil {
		return err
	}
	session := tunnelmux.NewSession(conn)
	defer session.Close()
	if a.OnConnected != nil {
		a.OnConnected()
	}

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		go a.handleStream(ctx, stream)
	}
}

func (a *Agent) handleStream(ctx context.Context, stream net.Conn) {
	defer stream.Close()
	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
		return
	}
	if err := a.HandleStream(ctx, req.WithContext(ctx), stream); err != nil {
		log.Printf("agent: stream handling failed: %v", err)
		_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
	}
}

func (a *Agent) register(ctx context.Context) error {
	payload := gatewayproto.RegisterRequest{
		Project: a.Project,
		Slug:    a.Slug,
		Label:   a.Label,
		AgentID: a.AgentID,
		Name:    a.Name,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	base, err := url.Parse(a.GatewayURL)
	if err != nil {
		return err
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/_agent/register"})
	query := endpoint.Query()
	query.Set("stream", "1")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", registerProgressMIME)
	client := a.HTTPClient
	if a.GatewayClient != nil {
		client = a.GatewayClient
	}
	client = clientWithMinimumTimeout(client, registerRequestTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("register failed: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out gatewayproto.RegisterResponse
	decoder := json.NewDecoder(resp.Body)
	for {
		var event gatewayproto.RegisterResponse
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("decode register response: %w", err)
		}
		if event.Type == "progress" {
			if a.OnRegisterProgress != nil {
				a.OnRegisterProgress(strings.TrimSpace(event.Stage), strings.TrimSpace(event.Message))
			}
			continue
		}
		out = event
		if event.Type == "result" {
			break
		}
		if event.Status != "" || event.PublicHost != "" || event.Error != "" || event.Code != "" {
			break
		}
	}

	if strings.EqualFold(strings.TrimSpace(out.Status), "error") || strings.TrimSpace(out.Code) != "" || strings.TrimSpace(out.Error) != "" {
		return fmt.Errorf("register failed: %s", formatRegisterFailure(out))
	}
	if a.OnRegistered != nil {
		a.OnRegistered(strings.TrimSpace(out.PublicHost))
	}
	return nil
}

func (a *Agent) connectTunnel(ctx context.Context) (net.Conn, error) {
	base, err := url.Parse(a.GatewayURL)
	if err != nil {
		return nil, err
	}
	address := base.Host
	if !strings.Contains(address, ":") {
		if base.Scheme == "https" {
			address += ":443"
		} else {
			address += ":80"
		}
	}
	var conn net.Conn
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if base.Scheme == "https" {
		tlsCfg := &tls.Config{
			ServerName: strings.Split(base.Host, ":")[0],
			MinVersion: tls.VersionTLS12,
		}
		if a.TLSConfig != nil {
			tlsCfg = a.TLSConfig.Clone()
			if tlsCfg.ServerName == "" {
				tlsCfg.ServerName = strings.Split(base.Host, ":")[0]
			}
			if tlsCfg.MinVersion == 0 {
				tlsCfg.MinVersion = tls.VersionTLS12
			}
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName:           tlsCfg.ServerName,
			MinVersion:           tlsCfg.MinVersion,
			Certificates:         tlsCfg.Certificates,
			GetClientCertificate: tlsCfg.GetClientCertificate,
			RootCAs:              tlsCfg.RootCAs,
			InsecureSkipVerify:   tlsCfg.InsecureSkipVerify,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, err
	}

	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)
	targetPath := "/_agent/tunnel/" + a.Label
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Path: targetPath},
		Host:   base.Host,
		Header: make(http.Header),
	}
	if err := req.Write(bw); err != nil {
		conn.Close()
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("connect tunnel failed: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	resp.Body.Close()
	return conn, nil
}

func UpstreamStreamHandler(upstreamURL string, client *http.Client) (func(context.Context, *http.Request, net.Conn) error, error) {
	parsed, err := url.Parse(strings.TrimSpace(upstreamURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("upstream URL must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return func(ctx context.Context, req *http.Request, stream net.Conn) error {
		outReq, err := rewriteForUpstream(req, parsed)
		if err != nil {
			return err
		}
		if isUpgradeRequest(outReq) {
			return forwardUpgradeToUpstream(outReq.WithContext(ctx), parsed, stream, client)
		}
		resp, err := doUpstreamRequest(client, outReq.WithContext(ctx))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return resp.Write(stream)
	}, nil
}

func forwardUpgradeToUpstream(req *http.Request, upstreamURL *url.URL, stream net.Conn, client *http.Client) error {
	upstreamConn, err := dialUpgradeUpstream(upstreamURL, client)
	if err != nil {
		return err
	}
	defer upstreamConn.Close()

	if err := req.Write(upstreamConn); err != nil {
		return err
	}
	upstreamReader := bufio.NewReader(upstreamConn)
	resp, err := http.ReadResponse(upstreamReader, req)
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
	return utils.ProxyBidirectional(stream, nil, upstreamConn, upstreamReader)
}

func dialUpgradeUpstream(upstreamURL *url.URL, client *http.Client) (net.Conn, error) {
	if upstreamURL == nil {
		return nil, errors.New("upstream URL is required")
	}
	address := upstreamURL.Host
	if !strings.Contains(address, ":") {
		switch strings.ToLower(upstreamURL.Scheme) {
		case "https":
			address += ":443"
		default:
			address += ":80"
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	switch strings.ToLower(upstreamURL.Scheme) {
	case "https":
		tlsCfg := tlsConfigForUpstreamDial(client, upstreamURL.Hostname())
		return tls.DialWithDialer(dialer, "tcp", address, tlsCfg)
	case "http", "":
		return dialer.Dial("tcp", address)
	default:
		return nil, fmt.Errorf("unsupported upstream scheme for upgrade: %s", upstreamURL.Scheme)
	}
}

func tlsConfigForUpstreamDial(client *http.Client, host string) *tls.Config {
	base := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	}
	if client == nil {
		return base
	}
	transport := client.Transport
	if transport == nil {
		return base
	}
	httpTransport, ok := transport.(*http.Transport)
	if !ok || httpTransport.TLSClientConfig == nil {
		return base
	}
	cloned := httpTransport.TLSClientConfig.Clone()
	if cloned.ServerName == "" {
		cloned.ServerName = host
	}
	if cloned.MinVersion == 0 {
		cloned.MinVersion = tls.VersionTLS12
	}
	return cloned
}

func doUpstreamRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	singleHop := *client
	singleHop.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return singleHop.Do(req)
}

func rewriteForUpstream(req *http.Request, upstream *url.URL) (*http.Request, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme = upstream.Scheme
	out.URL.Host = upstream.Host
	out.RequestURI = ""
	if req.Host == "" {
		out.Host = upstream.Host
	}
	return out, nil
}

func isUpgradeRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		r.Header.Get("Upgrade") != ""
}

func writeGatewayErrorResponse(w io.Writer, status int, err error) error {
	resp := &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(err.Error())),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(err.Error())))
	return resp.Write(w)
}

func formatRegisterFailure(out gatewayproto.RegisterResponse) string {
	code := strings.TrimSpace(out.Code)
	msg := strings.TrimSpace(out.Error)
	switch {
	case code != "" && msg != "":
		return code + ": " + msg
	case code != "":
		return code
	case msg != "":
		return msg
	default:
		return "unknown register error"
	}
}

func clientWithMinimumTimeout(client *http.Client, minTimeout time.Duration) *http.Client {
	if client == nil {
		return &http.Client{Timeout: minTimeout}
	}
	if client.Timeout >= minTimeout {
		return client
	}
	clone := *client
	clone.Timeout = minTimeout
	return &clone
}
