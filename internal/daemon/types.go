package daemon

import "dev/internal/agent"

type HealthResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

type WorktreeRequest struct {
	Slug      string   `json:"slug"`
	Project   string   `json:"project,omitempty"`
	Path      string   `json:"path,omitempty"`
	Processes []string `json:"processes,omitempty"`
	All       bool     `json:"all,omitempty"`
}

type TunnelRequest struct {
	Slug         string `json:"slug,omitempty"`
	Label        string `json:"label,omitempty"`
	GatewayURL   string `json:"gateway_url,omitempty"`
	Project      string `json:"project,omitempty"`
	Name         string `json:"name,omitempty"`
	AuthUsername string `json:"auth_username,omitempty"`
	AuthPassword string `json:"auth_password,omitempty"`
}

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

type TunnelsResponse struct {
	Tunnels []TunnelStatus `json:"tunnels"`
}

type ProxyTarget = agent.ProxyTarget
