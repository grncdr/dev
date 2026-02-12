package gatewayproto

// RegisterRequest is the JSON body sent by an agent to /_agent/register.
type RegisterRequest struct {
	// Project is the project name.
	Project string `json:"project"`
	// Slug is the worktree slug.
	Slug string `json:"slug"`
	// Label is the unique tunnel label to register.
	Label string `json:"label"`
	// AgentID is the unique agent instance identifier.
	AgentID string `json:"agent_id"`
	// Name is a human-readable agent name.
	Name string `json:"name"`
}

// RegisterResponse is the gateway's response to a registration request.
// When streamed (NDJSON), multiple responses are sent: progress events
// (Type="progress") followed by a final result (Type="result").
type RegisterResponse struct {
	// Type is "progress" for intermediate events or "result" for the final response.
	Type string `json:"type,omitempty"`
	// Stage identifies the current provisioning step (e.g. "dns_sync_started").
	Stage string `json:"stage,omitempty"`
	// Message is a human-readable description of the current stage.
	Message string `json:"message,omitempty"`
	// Status is the final outcome: "ok" or "error".
	Status string `json:"status,omitempty"`
	// Code is an error code when Status is "error".
	Code string `json:"code,omitempty"`
	// Error is the error message when Status is "error".
	Error string `json:"error,omitempty"`
	// PublicHost is the gateway's DNS zone (set on success).
	PublicHost string `json:"public_host,omitempty"`
	// PublicHostname is the full public hostname for this label (e.g. "label.zone.com").
	PublicHostname string `json:"public_hostname,omitempty"`
}
