package daemon

import "dev/internal/agent"

// HealthResponse is the JSON response from the /health daemon endpoint.
type HealthResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
	// Version is the daemon version string.
	Version string `json:"version,omitempty"`
}

// WorktreeRequest is the JSON body for worktree and process start/stop/status endpoints.
type WorktreeRequest struct {
	// Slug identifies the worktree.
	Slug string `json:"slug"`
	// Project narrows resolution when multiple projects share a slug.
	Project string `json:"project,omitempty"`
	// Path is a directory hint for resolving the worktree on disk.
	Path string `json:"path,omitempty"`
	// Processes limits the operation to specific process names.
	Processes []string `json:"processes,omitempty"`
	// All selects all processes when Processes is empty.
	All bool `json:"all,omitempty"`
}

// TunnelRequest is the JSON body for tunnel open/close endpoints.
// Fields map 1:1 to agent.TunnelSpec when the daemon opens a tunnel.
type TunnelRequest struct {
	// Slug identifies the worktree to tunnel.
	Slug string `json:"slug,omitempty"`
	// Label is the unique tunnel label registered with the gateway.
	Label string `json:"label,omitempty"`
	// GatewayURL is the gateway server URL to connect to.
	GatewayURL string `json:"gateway_url,omitempty"`
	// Project is the project name for gateway registration metadata.
	Project string `json:"project,omitempty"`
	// Name is a human-readable identifier sent to the gateway.
	Name string `json:"name,omitempty"`
	// AuthUsername is the HTTP basic auth username for access control.
	AuthUsername string `json:"auth_username,omitempty"`
	// AuthPassword is the HTTP basic auth password for access control.
	AuthPassword string `json:"auth_password,omitempty"`
}

// TunnelStatus is the JSON representation of a tunnel's current state,
// returned by the tunnels/status and tunnels/open endpoints.
// It is the API projection of agent.TunnelStatus.
type TunnelStatus struct {
	Slug            string `json:"slug"`
	Label           string `json:"label"`
	GatewayURL      string `json:"gateway_url"`
	PublicHost      string `json:"public_host,omitempty"`
	Project         string `json:"project,omitempty"`
	Status          string `json:"status"`
	LastError       string `json:"last_error,omitempty"`
	RegisterStage   string `json:"register_stage,omitempty"`
	RegisterMessage string `json:"register_message,omitempty"`
}

// TunnelsResponse wraps the list of tunnels returned by GET /tunnels/status.
type TunnelsResponse struct {
	Tunnels []TunnelStatus `json:"tunnels"`
}

type ProxyTarget = agent.ProxyTarget

// RunningWorktree is a lightweight reference for a worktree that currently has
// at least one running process managed by the daemon.
type RunningWorktree struct {
	Slug string `json:"slug"`
	Path string `json:"path,omitempty"`
}

// RunningWorktreesResponse wraps running worktree references returned by the
// GET /worktrees/running endpoint.
type RunningWorktreesResponse struct {
	Worktrees []RunningWorktree `json:"worktrees"`
}
