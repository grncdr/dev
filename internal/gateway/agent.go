package gateway

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

	"dev/internal/tunnelmux"
)

type Agent struct {
	GatewayURL     string
	UpstreamURL    string
	Project        string
	Slug           string
	Label          string
	AgentID        string
	Name           string
	RetryDelay     time.Duration
	HTTPClient     *http.Client
	GatewayClient  *http.Client
	UpstreamClient *http.Client
	TLSConfig      *tls.Config
	OnConnected    func()
	OnDisconnected func(error)
	OnRegistered   func(publicHost string)
}

func (a *Agent) Run(ctx context.Context) error {
	if a.GatewayURL == "" || a.UpstreamURL == "" || a.Label == "" {
		return errors.New("gateway_url, upstream_url, and label are required")
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

	upstreamURL, err := url.Parse(a.UpstreamURL)
	if err != nil {
		return err
	}
	client := a.HTTPClient
	if a.UpstreamClient != nil {
		client = a.UpstreamClient
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		go a.handleStream(ctx, client, upstreamURL, stream)
	}
}

func (a *Agent) handleStream(ctx context.Context, client *http.Client, upstreamURL *url.URL, stream net.Conn) {
	defer stream.Close()
	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
		return
	}
	outReq, err := rewriteForUpstream(req, upstreamURL)
	if err != nil {
		_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
		return
	}
	if isUpgradeRequest(outReq) {
		if err := forwardUpgradeToUpstream(outReq.WithContext(ctx), upstreamURL, stream, client); err != nil {
			log.Printf("gateway agent: upgrade forward failed: %v", err)
			_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
		}
		return
	}
	resp, err := doUpstreamRequest(client, outReq.WithContext(ctx))
	if err != nil {
		_ = writeGatewayErrorResponse(stream, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()
	_ = resp.Write(stream)
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
	return proxyBidirectional(stream, nil, upstreamConn, upstreamReader)
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
	// Tunnel forwarding must pass redirect responses through unchanged.
	singleHop := *client
	singleHop.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return singleHop.Do(req)
}

func (a *Agent) register(ctx context.Context) error {
	payload := RegisterRequest{
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.HTTPClient
	if a.GatewayClient != nil {
		client = a.GatewayClient
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		PublicHost string `json:"public_host"`
	}
	if resp.StatusCode < 300 {
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if a.OnRegistered != nil {
			a.OnRegistered(strings.TrimSpace(out.PublicHost))
		}
		return nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("register failed: %s %s", resp.Status, strings.TrimSpace(string(b)))
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
			ServerName:         tlsCfg.ServerName,
			MinVersion:         tlsCfg.MinVersion,
			Certificates:       tlsCfg.Certificates,
			RootCAs:            tlsCfg.RootCAs,
			InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
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
