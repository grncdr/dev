package daemon

import (
	"errors"
	"testing"

	"dev/internal/agent"
	"dev/internal/config"
	"dev/internal/router"
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
		Identifier: worktree.Identifier{Project: project, Slug: slug},
		Path:       path,
		MainPath:   mainPath,
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
			conn = agent.NewConnection(gatewayURL, "")
			agents[gatewayURL] = conn
		}
		conn.SeedTunnel(agent.TunnelStatus{
			Identifier:      mt.req.Identifier,
			Label:           mt.req.Label,
			GatewayURL:      gatewayURL,
			PublicHost:      mt.publicHost,
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

func resolveProxyTargetForTest(s *Server, host, path string, fromGateway bool) (network string, address string, process string, gatewayMode string, gatewayDebugLog string, targetSlug string, targetPath string, err error) {
	if s.manager == nil || s.manager.router == nil {
		return "", "", "", "", "", "", "", errors.New("proxy router unavailable")
	}
	res, err := s.manager.router.Resolve(host, path)
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	matcher := res.Matcher
	rule := s.manager.gatewayExposeRuleForRuntimeProcess(res.RuntimeKey, matcher.Process)
	if fromGateway && rule.Mode == config.GatewayModeDisable {
		return "", "", "", "", "", "", "", errGatewayProcessNotExposed
	}
	wt, ok := s.manager.WorktreeByRuntimeKey(res.RuntimeKey)
	if !ok {
		return "", "", "", "", "", "", "", router.ErrWorktreeNotMapped
	}
	targetSlug = wt.Slug
	targetPath = wt.Path
	if matcher.Singleton {
		mainSlug, err := resolveMainWorktreeSlug(targetPath)
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
		mainPath, err := worktree.ResolveMainPathInDir(targetPath)
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
		targetSlug = mainSlug
		targetPath = mainPath
	}
	s.manager.beginProxySessionFromDir(targetSlug, targetPath, matcher.Process)
	network, address, err = s.manager.EnsureProcessForTargetFromDir(targetSlug, targetPath, matcher.Process)
	if err != nil {
		s.manager.endProxySessionFromDir(targetSlug, targetPath, matcher.Process)
		return "", "", "", "", "", "", "", err
	}
	return network, address, matcher.Process, rule.Mode, rule.DebugLog, targetSlug, targetPath, nil
}
