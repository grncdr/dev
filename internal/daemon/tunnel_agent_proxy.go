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
	"dev/internal/router"
)

var errGatewayProcessNotExposed = errors.New("matched process does not allow gateway traffic")

// defaultSubdomainNoAuth reports whether the worktree's default-subdomain service
// opts out of share auth. Used on the base-host redirect path so a public default
// service is reachable from the bare label host without credentials.
func (s *Server) defaultSubdomainNoAuth(runtimeKey, baseLocalHost, defaultSubdomain string) bool {
	host := strings.TrimSpace(defaultSubdomain) + "." + strings.TrimSpace(baseLocalHost)
	res, err := s.manager.router.ResolveWithinWorktree(runtimeKey, host, "/")
	if err != nil || res.Matcher == nil {
		return false
	}
	rule := s.manager.gatewayExposeRuleForRuntimeProcess(runtimeKey, res.Matcher.Process)
	if rule.Mode == config.GatewayModeDisable {
		return false
	}
	return rule.NoAuth
}

func (s *Server) handleTunnelAgentRequest(ctx context.Context, tunnel agent.TunnelStatus, req *http.Request, stream net.Conn) error {
	resolvedPath, err := s.manager.resolveWorktreePath(tunnel.Slug, strings.TrimSpace(tunnel.Project), "")
	if err != nil {
		return err
	}

	runtimeKey := runtimeKeyForPath(resolvedPath)

	// routedMatcher is captured by ResolveRouting and reused by EnsureTarget so
	// the process is started only after the auth decision. Both callbacks run
	// sequentially within a single request, so no synchronization is needed.
	var routedMatcher *router.Matcher

	return agent.HandleTunnelRequest(ctx, agent.TunnelProxyOptions{
		ResolveRouting: func(host, path string) (agent.TunnelResolveResult, error) {
			result := agent.TunnelResolveResult{}
			if s.manager == nil {
				return result, errors.New("manager went away")
			}
			res, err := s.manager.router.ResolveWithinWorktree(runtimeKey, host, path)
			result.DefaultSubdomain = res.DefaultSubdomain
			if err != nil {
				if strings.TrimSpace(res.DefaultSubdomain) != "" {
					result.DefaultSubdomainNoAuth = s.defaultSubdomainNoAuth(runtimeKey, host, res.DefaultSubdomain)
				}
				return result, err
			}
			rule := s.manager.gatewayExposeRuleForRuntimeProcess(runtimeKey, res.Matcher.Process)
			if rule.Mode == config.GatewayModeDisable {
				return result, errGatewayProcessNotExposed
			}

			routedMatcher = res.Matcher
			result.GatewayMode = rule.Mode
			result.GatewayDebugLog = rule.DebugLog
			result.RewritePeerSubdomains = append([]string(nil), rule.RewritePeerSubdomains...)
			result.NoAuth = rule.NoAuth
			result.WebSocketPaths = append([]string(nil), rule.WebSocketPaths...)
			return result, nil
		},
		EnsureTarget: func(host string, _ agent.TunnelResolveResult) (agent.ProxyTarget, string, error) {
			target, err := s.manager.ensureProxyTargetForRuntime(runtimeKey, routedMatcher)
			if err != nil {
				return agent.ProxyTarget{}, "", err
			}
			resolvedLocalHost := host
			if h, ok := s.localProxyHostForTarget(host, target.Path); ok {
				resolvedLocalHost = h
			}
			return target, resolvedLocalHost, nil
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
