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
