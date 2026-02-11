package gatewayproto

type RegisterRequest struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
	Label   string `json:"label"`
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type RegisterResponse struct {
	Type           string `json:"type,omitempty"`
	Stage          string `json:"stage,omitempty"`
	Message        string `json:"message,omitempty"`
	Status         string `json:"status,omitempty"`
	Code           string `json:"code,omitempty"`
	Error          string `json:"error,omitempty"`
	PublicHost     string `json:"public_host,omitempty"`
	PublicHostname string `json:"public_hostname,omitempty"`
}
