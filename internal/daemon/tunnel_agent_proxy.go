package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"dev/internal/agent"
	"dev/internal/config"
)

var errGatewayProcessNotExposed = errors.New("matched process does not allow gateway traffic")

func (s *Server) handleTunnelAgentRequest(ctx context.Context, tunnel agent.TunnelStatus, req *http.Request, stream net.Conn) error {
	resolvedPath, err := s.manager.resolveWorktreePath(tunnel.Slug, strings.TrimSpace(tunnel.Project), "")
	if err != nil {
		return err
	}

	runtimeKey := runtimeKeyForPath(resolvedPath)

	return agent.HandleTunnelRequest(ctx, agent.TunnelProxyOptions{
		ResolveTarget: func(host, path string) (agent.TunnelResolveResult, error) {
			result := agent.TunnelResolveResult{}
			if s.manager == nil {
				return result, errors.New("manager went away")
			}
			matcher, err := s.manager.router.ResolveWithinWorktree(runtimeKey, host, path)
			if err != nil {
				return result, err
			}
			rule := s.manager.gatewayExposeRuleForRuntimeProcess(runtimeKey, matcher.Process)
			if rule.Mode == config.GatewayModeDisable {
				return result, errGatewayProcessNotExposed
			}

			result.GatewayMode = rule.Mode
			result.GatewayDebugLog = rule.DebugLog
			result.ProxyTarget, err = s.manager.ensureProxyTargetForRuntime(runtimeKey, matcher)

			return result, err
		},
		EndProxySession: func(targetSlug, targetPath, process string) {
			s.manager.endProxySessionFromDir(targetSlug, targetPath, process)
		},
		ProjectApexZone: s.projectApexZone,
		WorktreePath: func(targetSlug, targetPath string) (string, bool) {
			return s.manager.WorktreePathFromDir(targetSlug, targetPath)
		},
		Logf: func(format string, args ...any) {
			writeDaemonLogLine(fmt.Sprintf(format, args...))
		},
	}, tunnel, req, stream)
}
