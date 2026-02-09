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
- `local-dns.overrides` remaps worktree slugs to host labels for proxy/status routing.

## Config Model

```toml
[project]
name = "Foo Corp"
main_slug = "primary"

[local-dns]
overrides = { main = "foocorp" }

[process.rails]
command = "puma"
port = "random"                         # "random" | "unix" | integer
health = { type = "http", path = "/up" } # optional
startup_timeout = 45.0                  # optional, seconds
idle_timeout = 300.0                    # optional, seconds (proxied processes)
needs = ["postgres"]                    # optional

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
2. Resolve host label remaps (`local-dns.overrides`) to worktree slug when applicable.
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

## Idle Shutdown

For proxied processes (`[[process.<name>.proxy]]` configured), daemon tracks proxy activity and applies idle shutdown:

- Default idle timeout is `5m` (`300` seconds).
- Override per process with `idle_timeout` (decimal seconds).
- `idle_timeout = 0` disables idle shutdown for that process.
- If no proxied HTTP requests or TCP proxy connections are active/seen within the timeout, daemon stops the process.
- The next routed request/connection auto-starts the stopped process and waits for readiness before forwarding upstream.

## Raw TCP Forwarding

If matcher sets `tcp_listen`, daemon opens `127.0.0.1:<tcp_listen>` and forwards accepted TCP connections to the matched process target.

- TCP forwarding is process-scoped and auto-start/health-gated.
- Intended for services like Postgres/Redis/MySQL where HTTP routing is not used.

## Header and Response Handling

`gateway.expose` controls which processes accept gateway-tunneled traffic and how request/response handling behaves:

- `gateway.expose.<process>.mode = "reverse_proxy"`:
  - sets `X-Forwarded-Proto: https`
  - does not rewrite response headers/body
- `gateway.expose.<process>.mode = "rewrite"`:
  - keeps `X-Forwarded-Proto=https` and omits `X-Forwarded-Host` / `X-Forwarded-For`
  - rewrites absolute `Location` headers by replacing local apex with gateway public apex
  - rewrites `Set-Cookie` `Domain` values by replacing local apex with gateway public apex
  - rewrites incoming RFC cookie `$Domain` values from public apex back to local apex
  - rewrites text response bodies for local/public host mapping
- `gateway.expose.<process>.debug_log = "<path>"`:
  - appends full tunneled HTTP request/response transcripts to a file (response logging happens after rewrite handling)
  - relative paths resolve from the matched process worktree directory
- Processes not listed in `gateway.expose` reject gateway-tunneled requests.

## Gateway → Agent Flow

1. Client hits gateway URL.
2. Gateway selects agent by label and forwards stream.
3. Agent hands request to local proxy.
4. Local proxy applies the same routing logic described above.

Gateway does not perform subdomain/path matching logic.
