# WebSocket upgrade share-auth exemption — design

Extends per-service share-auth opt-out (see
[../adr/0001-per-service-share-auth-decision-point.md](../adr/0001-per-service-share-auth-decision-point.md))
with a path-scoped, upgrade-only exemption so browser WebSocket clients can
reach services like Action Cable, Vite HMR, or Phoenix LiveView through a
share-authed tunnel. Browsers cannot attach Basic Auth to a `WebSocket`
handshake, so even an already-authenticated user is rejected on the upgrade.

Also lifts the current "upgrade requests are not yet supported for direct
tunnel routing" rejection in `internal/agent/tunnel_proxy.go` so authenticated
WS works on any exposed service, not only the ones with the new flag.

## Config

New per-service key on the table form of `[gateway.expose.<service>]`:

```toml
[gateway.expose.rails]
mode = "rewrite"
websocket_paths = ["/cable"]
```

- Type: list of strings.
- Default: empty (no auth exemption).
- Match semantics: **segment-boundary prefix**. `/cable` matches `/cable` and
  `/cable/foo` but not `/cablecar`. Consistent with the default `match =
  "prefix"` semantics of routing matchers.
- Normalisation: trim whitespace, drop empties, ensure a single leading `/`.
- Only the table form carries this key. The bare-string mode shorthand
  (`web = "rewrite"`) cannot opt in. Same constraint as `auth`.
- No wildcard `"*"`. Opt-in was chosen for security; a wildcard would defeat
  that. Easy to add later if needed.
- If the service already has `auth = false`, `websocket_paths` is inert.

## Auth decision

In `HandleTunnelRequest`, after `ResolveRouting`:

```
isUpgrade := isUpgradeHTTPRequest(req)
authExempt := resolved.NoAuth ||
    (isUpgrade && pathMatchesAny(req.URL.Path, resolved.WebSocketPaths))

if !authExempt && !authenticated() {
    return writeTunnelAuthRequired(stream)
}
```

The unmatched-host redirect path is unchanged. A WS upgrade to an unmatched
host still produces a 401 to anonymous clients (existence is not leaked) and
the redirect itself still requires auth unless the default service is
`auth = false`.

`websocket_paths` is **only** an auth-exempt switch. It does not control
whether WS upgrade forwarding is supported (it always is, see below).

## Upgrade forwarding

`HandleTunnelRequest` currently rejects every upgrade request:

```go
if isUpgradeHTTPRequest(req) {
    return errors.New("upgrade requests are not yet supported for direct tunnel routing")
}
```

Lift this. After the auth decision and `EnsureTarget`, branch:

- Non-upgrade: existing HTTP proxy / rewrite path.
- Upgrade: new `forwardTunnelUpgrade(req, target ProxyTarget, stream net.Conn)
  error` helper modeled on `internal/agent/runner.go`'s
  `forwardUpgradeToUpstream`, but dialing `target.Network` / `target.Address`
  directly instead of an HTTP URL (the tunnel proxy already has a resolved
  dialable target). After the 101 response is written back to the client,
  proxy bidirectionally between client stream and upstream conn.

`rewrite` mode for upgrade requests: the handshake gets the same request-side
header translation as the regular HTTP path (Host, forwarded headers, and
public→local `Cookie`-domain / `Origin` / `Referer` translation) — upstreams
like Action Cable validate `Origin` on the handshake, so passing the public
host through would break them. A failed handshake (non-101) gets rewrite-mode
`Location` / `Set-Cookie` translation on the way back. Body rewrite does not
apply (the handshake has no body to rewrite, and the upgraded connection is
opaque); a 101 has no translatable headers.

## Module changes

- `internal/config/gateway_expose.go`: parse `websocket_paths`, normalise,
  attach `WebSocketPaths []string` to `GatewayExposeRule`.
- `internal/agent/tunnel_proxy.go`: add `WebSocketPaths []string` to
  `TunnelResolveResult`; rework the upgrade rejection into the auth-exempt
  branch and upgrade-forwarding call; add `forwardTunnelUpgrade` helper and a
  segment-boundary path-match helper.
- `internal/daemon/tunnel_agent_proxy.go`: copy `rule.WebSocketPaths` into
  `TunnelResolveResult` inside `ResolveRouting`.
- `docs/config.md`: document the key and its match semantics.

## Tests

- `internal/config/gateway_expose_test.go`:
  - `websocket_paths = ["/cable", "/live"]` → normalised list on the rule.
  - Bare-string mode → no `WebSocketPaths`.
  - Path missing leading `/` is normalised.
- `internal/agent/tunnel_proxy_test.go`:
  - WS upgrade to a path in `WebSocketPaths` on an authenticated service
    succeeds without credentials and 101 is forwarded.
  - WS upgrade to a non-listed path on the same service returns 401 without
    credentials.
  - Plain HTTP `GET` to a path in `WebSocketPaths` still requires auth
    (the flag is upgrade-only).
  - Authenticated WS upgrade on a service with no `WebSocketPaths` succeeds
    (the unconditional rejection is gone).
  - `EnsureTarget` is not called when an unauthenticated, non-matching WS
    upgrade is rejected (auth-before-process invariant preserved).
  - Anonymous WS upgrade to an unmatched host gets 401, not a redirect, even
    when a `default_subdomain` is configured (no existence leak via upgrade).

## Out of scope

- Path-based exemption for non-upgrade HTTP. Defer until there is a concrete
  use case.
- Wildcard `"*"` in `websocket_paths`.
- Per-path or per-route credentials. Only opt-out is supported.
- Logging or CLI surface for the exemption (silent, matching `auth = false`).
