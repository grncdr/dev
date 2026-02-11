package daemon

import (
	"testing"

	"dev/internal/agent"
	"dev/internal/config"
	"dev/internal/worktree"
)

type managedTunnel struct {
	req             TunnelRequest
	status          string
	publicHost      string
	lastError       string
	registerStage   string
	registerMessage string
}

func defaultSlugForRepo(t *testing.T, repoDir string) string {
	t.Helper()
	slug, err := worktree.ResolveDefaultSlug(repoDir, nil)
	if err != nil {
		t.Fatalf("resolve default slug: %v", err)
	}
	return slug
}

func registerWorktreeForTest(t *testing.T, daemonCfg *config.DaemonConfig, project, slug, path, mainPath string) {
	t.Helper()
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project:  project,
		Slug:     slug,
		Path:     path,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register worktree: %v", err)
	}
}

func seededAgentsForTunnels(tunnels map[string]*managedTunnel) map[string]*agent.Connection {
	agents := map[string]*agent.Connection{}
	for _, mt := range tunnels {
		if mt == nil {
			continue
		}
		gatewayURL := mt.req.GatewayURL
		if gatewayURL == "" {
			gatewayURL = "https://gateway.example.test"
		}
		conn, ok := agents[gatewayURL]
		if !ok || conn == nil {
			conn = agent.NewConnection(gatewayURL)
			agents[gatewayURL] = conn
		}
		conn.SeedTunnel(agent.TunnelStatus{
			Slug:            mt.req.Slug,
			Label:           mt.req.Label,
			GatewayURL:      gatewayURL,
			PublicHost:      mt.publicHost,
			Project:         mt.req.Project,
			Status:          mt.status,
			LastError:       mt.lastError,
			RegisterStage:   mt.registerStage,
			RegisterMessage: mt.registerMessage,
			AuthUsername:    mt.req.AuthUsername,
			AuthPassword:    mt.req.AuthPassword,
		})
	}
	return agents
}
