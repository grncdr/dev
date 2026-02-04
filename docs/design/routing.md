# Request Routing

This document is the source of truth for local proxy routing and gateway-to-agent request flow.

## Overview

- Routing is defined per process via `[[process.<name>.proxy]]` matchers.
- Gateway remains dumb: it only forwards traffic to the local agent/proxy.
- Local proxy handles HTTP(S) routing and optional raw TCP forwarding.
- Routed processes are auto-started on demand and health-gated before forwarding.

## Host Model

- Hostnames are matched case-insensitively.
- Effective apex zone comes from daemon config `local-proxy.apex_zone` (default `.localhost`).
- Request hosts are interpreted as:
  - `<slug>.<apex_zone>` (base worktree host)
  - `<subdomain>.<slug>.<apex_zone>` (subdomain-of-slug host)
- `project.main_slug` applies only to main worktree host mapping.

## Config Model

```toml
[project]
name = "Foo Corp"
main_slug = "foocorp"

[process.rails]
command = "puma"
port = "random"                         # "random" | "unix" | integer
health = { type = "http", path = "/up" } # optional
startup_timeout = 45.0                  # optional, seconds

[[process.rails.proxy]]
subdomain = null
path = "/"
match = "prefix"                        # prefix (default) | exact
priority = 0

[[process.rails.proxy]]
subdomains = ["app", "secure"]
path = "/admin"
match = "exact"

[process.webpack]
command = "yarn webpack serve"
port = "random"

[[process.webpack.proxy]]
subdomains = ["app", "secure"]
path = "/ws"
match = "exact"

[[process.webpack.proxy]]
subdomains = ["app", "secure"]
path = "/packs/"                        # normalized to "/packs" for prefix matching

[process.postgres]
singleton = true
command = "postgres -k $PGHOST -p $PORT"
port = "random"
health = { type = "tcp" }

[[process.postgres.proxy]]
tcp_listen = 15432                      # raw TCP forwarder on 127.0.0.1:15432
```

## Matcher Fields

- `subdomain`: one subdomain value (`null`, `"*"`, or explicit string).
- `subdomains`: list form of `subdomain`; expands to multiple equivalent matchers.
- `path`: HTTP path pattern (default `/`).
- `match`: `prefix` (default) or `exact`.
- `priority`: integer, higher wins tie-breaks.
- `tcp_listen`: optional raw TCP listener port on `127.0.0.1`.

## HTTP Match Semantics

- `match = "exact"`: request path must equal `path`.
- `match = "prefix"`:
  - segment-boundary prefix matching (`/foo` matches `/foo` and `/foo/...`).
  - trailing slashes in pattern are normalized (`/packs/` behaves as `/packs`).
  - `*` suffix enables raw prefix semantics (`/foo*`).

## HTTP Route Selection

Given request `(host, path)`:

1. Parse `(slug, subdomain-of-slug)` from host.
2. Resolve main-slug alias (`project.main_slug`) to real main worktree slug when applicable.
3. Build candidate matchers for all processes.
4. Filter candidates by subdomain + path match.
5. Choose winner by:
   1. subdomain specificity (`explicit` > `*` > base/null)
   2. longest path match
   3. highest `priority`
   4. stable process-name order
6. Route to winning process target.

## Process Target Resolution

Each process target is derived from `port`:

- `port = "unix"` -> unix socket `${WORKTREE_STATE}/<process>.sock`
- `port = "random"` -> allocated high localhost TCP port
- `port = <integer>` -> fixed localhost TCP port

If process has proxy matchers and no `port`, it defaults to `"random"`.

`singleton = true` routes to main worktree process instance.

## On-Demand Start + Readiness

When routing selects a process that is not running:

1. Daemon starts it once (deduplicated by manager state).
2. Daemon waits for readiness:
   - `health = { type = "http", path = ... }` -> HTTP probe (2xx/3xx accepted)
   - `health = { type = "tcp" }` -> TCP connect probe
   - no health block -> TCP readiness probe on resolved target
3. Timeout defaults to 30s, override with `startup_timeout` (decimal seconds).
4. Request is forwarded only after readiness succeeds.

## Raw TCP Forwarding

If matcher sets `tcp_listen`, daemon opens `127.0.0.1:<tcp_listen>` and forwards accepted TCP connections to the matched process target.

- TCP forwarding is process-scoped and auto-start/health-gated.
- Intended for services like Postgres/Redis/MySQL where HTTP routing is not used.

## Header and Response Handling

Per-process `gateway_mode` controls request/response behavior for gateway-tunneled traffic:

- `gateway_mode = "reverse_proxy"` (default):
  - sets `X-Forwarded-Proto: https`
  - does not rewrite response headers/body
- `gateway_mode = "rewrite"`:
  - keeps `X-Forwarded-Proto=https` and omits `X-Forwarded-Host` / `X-Forwarded-For`
  - rewrites absolute `Location` headers by replacing local apex with gateway public apex
  - rewrites `Set-Cookie` `Domain` values by replacing local apex with gateway public apex
  - rewrites incoming RFC cookie `$Domain` values from public apex back to local apex
  - rewrites text response bodies for local/public host mapping

## Gateway → Agent Flow

1. Client hits gateway URL.
2. Gateway selects agent by label and forwards stream.
3. Agent hands request to local proxy.
4. Local proxy applies the same routing logic described above.

Gateway does not perform subdomain/path matching logic.
