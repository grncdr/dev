package daemon

type HealthResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

type WorktreeRequest struct {
	Slug      string   `json:"slug"`
	Path      string   `json:"path,omitempty"`
	Processes []string `json:"processes,omitempty"`
	All       bool     `json:"all,omitempty"`
}

type TunnelRequest struct {
	Slug       string `json:"slug,omitempty"`
	Label      string `json:"label,omitempty"`
	GatewayURL string `json:"gateway_url,omitempty"`
	Project    string `json:"project,omitempty"`
	Name       string `json:"name,omitempty"`
	Upstream   string `json:"upstream,omitempty"`
}

type TunnelStatus struct {
	Slug       string `json:"slug"`
	Label      string `json:"label"`
	GatewayURL string `json:"gateway_url"`
	Project    string `json:"project,omitempty"`
	Status     string `json:"status"`
	LastError  string `json:"last_error,omitempty"`
}

type TunnelsResponse struct {
	Tunnels []TunnelStatus `json:"tunnels"`
}
