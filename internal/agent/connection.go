package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"dev/internal/gateway"
)

type TunnelSpec struct {
	Slug          string
	Label         string
	GatewayURL    string
	Project       string
	Name          string
	UpstreamURL   string
	LocalBaseHost string
	AuthUsername  string
	AuthPassword  string
}

type TunnelStatus struct {
	Slug            string
	Label           string
	GatewayURL      string
	PublicHost      string
	Project         string
	Status          string
	LastError       string
	RegisterStage   string
	RegisterMessage string
	LocalBaseHost   string
	AuthUsername    string
	AuthPassword    string
}

type tunnelRuntime struct {
	spec          TunnelSpec
	cancel        context.CancelFunc
	status        string
	publicHost    string
	lastError     string
	registerStage string
	registerMsg   string
}

type Connection struct {
	gatewayURL string
	mu         sync.Mutex
	tunnels    map[string]*tunnelRuntime // label -> runtime
}

func NewConnection(gatewayURL string) *Connection {
	return &Connection{
		gatewayURL: strings.TrimSpace(gatewayURL),
		tunnels:    map[string]*tunnelRuntime{},
	}
}

func (c *Connection) GatewayURL() string {
	return c.gatewayURL
}

func (c *Connection) Open(spec TunnelSpec, gatewayClient *http.Client, gatewayTLS *tls.Config, upstreamClient *http.Client) (TunnelStatus, error) {
	spec.Label = strings.TrimSpace(spec.Label)
	spec.Slug = strings.TrimSpace(spec.Slug)
	spec.GatewayURL = strings.TrimSpace(spec.GatewayURL)
	spec.LocalBaseHost = strings.TrimSpace(strings.ToLower(spec.LocalBaseHost))
	if spec.Label == "" || spec.Slug == "" || spec.GatewayURL == "" || spec.UpstreamURL == "" {
		return TunnelStatus{}, errors.New("invalid tunnel spec")
	}
	if spec.GatewayURL != c.gatewayURL {
		return TunnelStatus{}, fmt.Errorf("gateway url mismatch: %s != %s", spec.GatewayURL, c.gatewayURL)
	}

	c.mu.Lock()
	for label, running := range c.tunnels {
		if label == spec.Label {
			status := c.toStatusLocked(running)
			c.mu.Unlock()
			return status, nil
		}
		if strings.EqualFold(running.spec.Slug, spec.Slug) {
			c.mu.Unlock()
			return TunnelStatus{}, fmt.Errorf("slug %s already has label %s", spec.Slug, label)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt := &tunnelRuntime{
		spec:   spec,
		cancel: cancel,
		status: "connecting",
	}
	c.tunnels[spec.Label] = rt
	c.mu.Unlock()

	agent := &gateway.Agent{
		GatewayURL:     spec.GatewayURL,
		UpstreamURL:    spec.UpstreamURL,
		Project:        spec.Project,
		Slug:           spec.Slug,
		Label:          spec.Label,
		AgentID:        fmt.Sprintf("dev-%d", time.Now().UnixNano()),
		Name:           spec.Name,
		RetryDelay:     500 * time.Millisecond,
		GatewayClient:  gatewayClient,
		TLSConfig:      gatewayTLS,
		UpstreamClient: upstreamClient,
		OnConnected: func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if cur, ok := c.tunnels[spec.Label]; ok {
				cur.status = "connected"
				cur.lastError = ""
			}
		},
		OnRegistered: func(publicHost string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if cur, ok := c.tunnels[spec.Label]; ok {
				cur.publicHost = strings.TrimSpace(publicHost)
				cur.registerStage = "register_complete"
				cur.registerMsg = "gateway registration complete"
			}
		},
		OnRegisterProgress: func(stage, message string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if cur, ok := c.tunnels[spec.Label]; ok {
				cur.registerStage = strings.TrimSpace(stage)
				cur.registerMsg = strings.TrimSpace(message)
			}
		},
		OnDisconnected: func(err error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if cur, ok := c.tunnels[spec.Label]; ok {
				cur.status = "connecting"
				if err != nil {
					cur.lastError = err.Error()
				}
			}
		},
	}

	go func() {
		err := agent.Run(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		cur, ok := c.tunnels[spec.Label]
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

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toStatusLocked(rt), nil
}

// SeedTunnel is for daemon-side bootstrap/testing when no live gateway session
// should be started.
func (c *Connection) SeedTunnel(status TunnelStatus) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	label := strings.TrimSpace(status.Label)
	if label == "" {
		return
	}
	gatewayURL := strings.TrimSpace(status.GatewayURL)
	if gatewayURL == "" {
		gatewayURL = c.gatewayURL
	}
	c.tunnels[label] = &tunnelRuntime{
		spec: TunnelSpec{
			Slug:          strings.TrimSpace(status.Slug),
			Label:         label,
			GatewayURL:    gatewayURL,
			Project:       strings.TrimSpace(status.Project),
			LocalBaseHost: strings.TrimSpace(strings.ToLower(status.LocalBaseHost)),
			AuthUsername:  strings.TrimSpace(status.AuthUsername),
			AuthPassword:  status.AuthPassword,
		},
		cancel:        func() {},
		status:        strings.TrimSpace(status.Status),
		publicHost:    strings.TrimSpace(status.PublicHost),
		lastError:     strings.TrimSpace(status.LastError),
		registerStage: strings.TrimSpace(status.RegisterStage),
		registerMsg:   strings.TrimSpace(status.RegisterMessage),
	}
}

func (c *Connection) Close(label, slug string) (TunnelStatus, error) {
	label = strings.TrimSpace(label)
	slug = strings.TrimSpace(slug)
	if label == "" && slug == "" {
		return TunnelStatus{}, errors.New("label or slug is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tunnels) == 0 {
		return TunnelStatus{}, errors.New("tunnel not found")
	}
	if label == "" {
		for key, rt := range c.tunnels {
			if strings.EqualFold(rt.spec.Slug, slug) {
				label = key
				break
			}
		}
	}
	rt, ok := c.tunnels[label]
	if !ok {
		return TunnelStatus{}, errors.New("tunnel not found")
	}
	rt.cancel()
	delete(c.tunnels, label)
	status := c.toStatusLocked(rt)
	status.Status = "stopped"
	return status, nil
}

func (c *Connection) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for label, rt := range c.tunnels {
		rt.cancel()
		delete(c.tunnels, label)
	}
}

func (c *Connection) RunningRequests() []TunnelSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]TunnelSpec, 0, len(c.tunnels))
	for _, rt := range c.tunnels {
		if rt == nil {
			continue
		}
		spec := rt.spec
		if spec.Label == "" || spec.Slug == "" || spec.GatewayURL == "" {
			continue
		}
		out = append(out, spec)
	}
	return out
}

func (c *Connection) Statuses() []TunnelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]TunnelStatus, 0, len(c.tunnels))
	for _, rt := range c.tunnels {
		out = append(out, c.toStatusLocked(rt))
	}
	return out
}

func (c *Connection) StatusForSlug(slug string) *TunnelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	var selected *TunnelStatus
	for _, rt := range c.tunnels {
		if !strings.EqualFold(rt.spec.Slug, slug) {
			continue
		}
		cur := c.toStatusLocked(rt)
		if selected == nil || (selected.Status != "connected" && cur.Status == "connected") {
			copied := cur
			selected = &copied
		}
	}
	return selected
}

func (c *Connection) TunnelForLabel(label string) (TunnelStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt, ok := c.tunnels[strings.TrimSpace(label)]
	if !ok {
		return TunnelStatus{}, false
	}
	return c.toStatusLocked(rt), true
}

func (c *Connection) Empty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tunnels) == 0
}

func (c *Connection) toStatusLocked(rt *tunnelRuntime) TunnelStatus {
	if rt == nil {
		return TunnelStatus{}
	}
	return TunnelStatus{
		Slug:            rt.spec.Slug,
		Label:           rt.spec.Label,
		GatewayURL:      rt.spec.GatewayURL,
		PublicHost:      rt.publicHost,
		Project:         rt.spec.Project,
		Status:          rt.status,
		LastError:       rt.lastError,
		RegisterStage:   rt.registerStage,
		RegisterMessage: rt.registerMsg,
		LocalBaseHost:   rt.spec.LocalBaseHost,
		AuthUsername:    rt.spec.AuthUsername,
		AuthPassword:    rt.spec.AuthPassword,
	}
}
