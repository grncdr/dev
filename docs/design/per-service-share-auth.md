# Per-service share auth opt-out — implementation plan

Lets an individual exposed service opt out of share auth while siblings on the same
tunnel stay protected. Config: `[gateway.expose.<service>] auth = false` (default `true`).

See [ADR 0001](../adr/0001-per-service-share-auth-decision-point.md) for the rationale
behind the auth decision-point change.

## 1. Config: parse the per-service flag

**`internal/config/gateway_expose.go`**

- Add `NoAuth bool` to `GatewayExposeRule` (zero value `false` = auth required, so the
  secure default is the zero value).
- In `parseGatewayExposeRule`, in **both** table branches (mode-present and mode-absent),
  read the `auth` key: if it is present and parses to boolean `false`, set `NoAuth = true`.
  The bare-string form (`web = "rewrite"`) cannot carry `auth`, so it never opts out.
- `disable` mode is filtered out before this matters (`GatewayExposeRules`), so
  `auth = false` on a disabled service is simply inert.

Test (`gateway_expose_test.go`): `auth = false` → `NoAuth == true`; omitted/`true` → `false`;
bare-string rule → `false`.

## 2. Split routing from process-ensure; carry the auth flags

**`internal/agent/tunnel_proxy.go`**

- Add to `TunnelResolveResult`:
  - `NoAuth bool` — resolved service opts out of auth.
  - `DefaultSubdomainNoAuth bool` — the default subdomain's service opts out (used only on
    the redirect path).
- Replace the single `ResolveTarget` callback in `TunnelProxyOptions` with two:
  - `ResolveRouting(host, path) (TunnelResolveResult, error)` — routing + rule lookup only,
    **no process start**. Populates `NoAuth`, `GatewayMode`, `GatewayDebugLog`,
    `RewritePeerSubdomains`, `DefaultSubdomain`, `DefaultSubdomainNoAuth`.
  - `EnsureTarget(TunnelResolveResult) (ProxyTarget, error)` — `ensureProxyTargetForRuntime`
    + `ResolvedLocalHost`, called only after auth passes.
- Rework `HandleTunnelRequest` control flow:

  ```
  resolved, err := opts.ResolveRouting(localHost, path)
  if err != nil {
      if location, ok := defaultSubdomainRedirectForTunnel(...); ok {
          if !resolved.DefaultSubdomainNoAuth && !authenticate(...) {
              return writeTunnelAuthRequired(stream)
          }
          return writeTunnelRedirect(stream, location)
      }
      if !authenticate(...) { return writeTunnelAuthRequired(stream) } // don't leak existence
      return err
  }
  if !resolved.NoAuth && !authenticate(...) {
      return writeTunnelAuthRequired(stream)
  }
  target, err := opts.EnsureTarget(resolved)
  // ... unchanged proxy/rewrite/transcript logic from here
  ```

  (`authenticate(...)` = the existing `authenticateGatewayTunnelCredentials(req,
  tunnel.AuthUsername, tunnel.AuthPassword)`.)

## 3. Implement the callbacks in the daemon

**`internal/daemon/tunnel_agent_proxy.go`**

- `ResolveRouting`: keep the existing `router.ResolveWithinWorktree` +
  `gatewayExposeRuleForRuntimeProcess`; set `result.NoAuth = rule.NoAuth` and
  `result.DefaultSubdomain = res.DefaultSubdomain`. Drop the `ensureProxyTargetForRuntime`
  call from here.
- On the error path where `res.DefaultSubdomain != ""`, resolve
  `<DefaultSubdomain>.<localBase>` through `router.ResolveWithinWorktree`, look up its rule
  via `gatewayExposeRuleForRuntimeProcess`, and set `result.DefaultSubdomainNoAuth`.
- `EnsureTarget`: the moved `ensureProxyTargetForRuntime` + `localProxyHostForTarget`
  (`ResolvedLocalHost`) logic.

## 4. Docs

**`docs/config.md`** — document `[gateway.expose.<service>] auth = false`: default is
`true`; requires the table form; opting out makes that one service publicly reachable with
no Basic Auth even when `[gateway.auth]` / `--auth` is set for the tunnel.

## 5. Tests (`internal/agent` / `internal/daemon`)

- Opted-out service is served with no credentials.
- Authenticated sibling on the same tunnel still returns 401 without credentials.
- Unmatched host returns 401 to an anonymous client (no existence leak).
- Base-host redirect: requires auth when the default service is authenticated; emits the
  302 with no auth when the default service is `auth = false`.
- `EnsureTarget` is **not** called for an unauthenticated request to an authenticated
  service (process is not started pre-auth).

## Out of scope

- No CLI summary or daemon logging of the waiver (deliberate — silent opt-out).
- No per-service *credentials* (only opt-out); the tunnel keeps one credential pair for all
  authenticated services.
