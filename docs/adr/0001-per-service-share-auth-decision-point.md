# Share auth is decided after routing resolution, not as an up-front gate

## Status

accepted

## Context

Share auth (HTTP Basic Auth on inbound public tunnel requests) was enforced as the
first thing in `HandleTunnelRequest`, before the request was routed to a service. To
let an individual service opt out of auth (`[gateway.expose.<service>] auth = false`),
the auth decision must know *which* service the request resolves to — which is only
known after routing. So the decision point moves from "up-front gate" to "after routing
resolution, before the service's process is started."

## Decision

Resolve routing first (cheap, no side effects), then decide auth, then ensure the
target process is running. Auth is **default-secure**: required for every request
*except* one that successfully resolves to a service whose expose rule sets
`auth = false`. Unmatched hosts and resolution errors keep requiring auth, exactly as
before. The bare-base-host default-subdomain redirect resolves the default service's
rule and is emitted without auth only when that service is public — otherwise it too
requires auth.

## Considered options

- **Keep auth as an up-front gate.** Simplest, but cannot express per-service opt-out
  at all — auth can only be tunnel-wide.
- **Gate auth strictly on the resolved rule, let errors fall through unauthenticated.**
  Simpler than default-secure, but leaks service existence to anonymous clients and would
  let the base-host path serve without auth.
- **Resolve the full target (routing + process start) before auth.** Would start a
  service's process for an anonymous request to an authenticated service — a new
  unauthenticated resource/DoS vector.

## Consequences

- The expose rule is looked up in two places (normal path and the default-subdomain
  redirect path); both reuse the same `gatewayExposeRuleForRuntimeProcess` helper, so it
  is reuse rather than duplicated logic.
- The base-host redirect path performs one extra routing resolve when the default service
  is involved; it is a cold path (hit once when a visitor lands on the bare URL).
- Anonymous requests still cannot start processes — the process-ensure step stays behind
  the auth decision.
