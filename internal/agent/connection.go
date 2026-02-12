package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TunnelSpec describes the parameters needed to open a tunnel through a gateway.
type TunnelSpec struct {
	// Slug is the worktree slug being tunneled.
	Slug string
	// Label is the unique tunnel label registered with the gateway.
	Label string
	// GatewayURL is the gateway server URL to connect to.
	GatewayURL string
	// Project is the project name for gateway registration.
	Project string
	// Name is a human-readable identifier sent to the gateway.
	Name string
	// LocalBaseHost is the local proxy hostname used to translate public
	// hostnames back to local routing (e.g. "main.localhost").
	// Derived from the daemon's apex zone and the worktree's DNS label.
	LocalBaseHost string
	// AuthUsername is the HTTP basic auth username for tunnel access control.
	AuthUsername string
	// AuthPassword is the HTTP basic auth password for tunnel access control.
	AuthPassword string
}

// TunnelStatus is the live state of a tunnel within a Connection.
type TunnelStatus struct {
	// Slug is the worktree slug being tunneled.
	Slug string
	// Label is the tunnel label registered with the gateway.
	Label string
	// GatewayURL is the gateway server this tunnel connects to.
	GatewayURL string
	// PublicHost is the gateway-assigned public hostname (set after registration).
	PublicHost string
	// Project is the project name.
	Project string
	// Status is the tunnel state: "connecting", "connected", "error", or "stopped".
	Status string
	// LastError holds the most recent connection or registration error.
	LastError string
	// RegisterStage is the current registration progress stage.
	RegisterStage string
	// RegisterMessage is the current registration progress message.
	RegisterMessage string
	// LocalBaseHost is the local proxy hostname for host translation.
	LocalBaseHost string
	// AuthUsername is the HTTP basic auth username for tunnel access control.
	AuthUsername string
	// AuthPassword is the HTTP basic auth password for tunnel access control.
	AuthPassword string
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

// Connection manages the set of tunnels to a single gateway server.
// It handles opening, closing, and tracking tunnel lifecycle for all
// worktrees sharing the same gateway URL.
type Connection struct {
	gatewayURL    string
	credentialDir string
	mu            sync.Mutex
	tunnels       map[string]*tunnelRuntime // label -> runtime
}

type TunnelRequestHandler func(context.Context, TunnelStatus, *http.Request, net.Conn) error

func NewConnection(gatewayURL, credentialDir string) *Connection {
	return &Connection{
		gatewayURL:    strings.TrimSpace(gatewayURL),
		credentialDir: strings.TrimSpace(credentialDir),
		tunnels:       map[string]*tunnelRuntime{},
	}
}

func (c *Connection) GatewayURL() string {
	return c.gatewayURL
}

func (c *Connection) Open(spec TunnelSpec, requestHandler TunnelRequestHandler) (TunnelStatus, error) {
	spec.Label = strings.TrimSpace(spec.Label)
	spec.Slug = strings.TrimSpace(spec.Slug)
	spec.GatewayURL = strings.TrimSpace(spec.GatewayURL)
	spec.LocalBaseHost = strings.TrimSpace(strings.ToLower(spec.LocalBaseHost))
	if spec.Label == "" || spec.Slug == "" || spec.GatewayURL == "" {
		return TunnelStatus{}, errors.New("invalid tunnel spec")
	}
	if requestHandler == nil {
		return TunnelStatus{}, errors.New("tunnel request handler is required")
	}
	if spec.GatewayURL != c.gatewayURL {
		return TunnelStatus{}, fmt.Errorf("gateway url mismatch: %s != %s", spec.GatewayURL, c.gatewayURL)
	}
	gatewayClient, gatewayTLS, err := MTLSClientForGatewayURL(spec.GatewayURL, c.credentialDir)
	if err != nil {
		return TunnelStatus{}, err
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

	runner := &Agent{
		GatewayURL:    spec.GatewayURL,
		Project:       spec.Project,
		Slug:          spec.Slug,
		Label:         spec.Label,
		AgentID:       fmt.Sprintf("dev-%d", time.Now().UnixNano()),
		Name:          spec.Name,
		RetryDelay:    500 * time.Millisecond,
		GatewayClient: gatewayClient,
		TLSConfig:     gatewayTLS,
		HandleStream: func(ctx context.Context, req *http.Request, stream net.Conn) error {
			c.mu.Lock()
			rt := c.tunnels[spec.Label]
			var status TunnelStatus
			if rt != nil {
				status = c.toStatusLocked(rt)
			}
			c.mu.Unlock()
			return requestHandler(ctx, status, req, stream)
		},
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
		err := runner.Run(ctx)
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
