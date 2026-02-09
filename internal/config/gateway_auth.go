package config

import (
	"errors"
	"strings"
)

type ProjectGatewayAuth struct {
	Username string
	Password string
}

var errProjectGatewayAuthIncomplete = errors.New("gateway.auth requires both username and password")

// ProjectGatewayAuthCredentials returns parsed project-level gateway auth defaults.
// It returns enabled=false when no auth table is configured. It returns an error
// when only one of username/password is provided.
func ProjectGatewayAuthCredentials(cfg *ProjectConfig) (creds ProjectGatewayAuth, enabled bool, err error) {
	if cfg == nil || cfg.Gateway == nil {
		return ProjectGatewayAuth{}, false, nil
	}
	rawAuth, ok := cfg.Gateway["auth"]
	if !ok || rawAuth == nil {
		return ProjectGatewayAuth{}, false, nil
	}
	authMap, ok := normalizeStringMap(rawAuth)
	if !ok {
		return ProjectGatewayAuth{}, false, nil
	}
	if val, ok := authMap["username"].(string); ok {
		creds.Username = strings.TrimSpace(val)
	}
	if val, ok := authMap["password"].(string); ok {
		creds.Password = val
	}
	if strings.TrimSpace(creds.Username) == "" && strings.TrimSpace(creds.Password) == "" {
		return ProjectGatewayAuth{}, false, nil
	}
	if strings.TrimSpace(creds.Username) == "" || strings.TrimSpace(creds.Password) == "" {
		return ProjectGatewayAuth{}, false, errProjectGatewayAuthIncomplete
	}
	return creds, true, nil
}
