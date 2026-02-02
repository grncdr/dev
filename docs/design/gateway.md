# Gateway Design (Draft)

This document specifies the first implementation of the public gateway and local agent tunnel.

## Goals

- Keep gateway routing simple: gateway forwards by `label`, local agent handles all host/path routing.
- Support stable public HTTPS URLs for a connected worktree.
- Preserve local proxy behavior and config as the single source of routing truth.
- Keep transport secure with mTLS between gateway and local agent.
- Support optional HTTP Basic Auth at the gateway edge for public tunnel access.

## Non-Goals (v1)

- No host/path routing decisions in gateway.
- No per-request dynamic policy engine in gateway.
- No per-label auth policies in v1 (auth is gateway-global).

## Terminology

- **Gateway**: public service that accepts client HTTP(S) and forwards to connected agents.
- **Agent**: code running in `dev-mode daemon` that maintains outbound tunnel connections.
- **Label**: globally unique routing key (defaults to worktree slug).
- **Public host**: `<subdomain>.<label>.<gateway_apex>`.

## Architecture

1. Local daemon opens one or more long-lived outbound mTLS streams to gateway.
2. Agent registers `{project, slug, label}`.
3. Gateway stores active registrations in memory (plus optional persistent lease metadata).
4. Public HTTP(S) request arrives at gateway.
5. Gateway extracts `label` from host and picks connected agent.
6. Gateway forwards request stream to that agent.
7. Agent injects request into local proxy listener.
8. Local proxy applies process matchers and startup/health gating.
9. Response streams back through agent to gateway to client.

Gateway persists tunnel/agent lease state to local files so active labels can be restored after restart.

## Routing Responsibility Split

- **Gateway**:
  - Parse host to identify `label`.
  - Select healthy connected agent for label.
  - Forward bytes/HTTP semantics.
- **Local proxy**:
  - Match subdomain/path to process.
  - Start stopped process on demand.
  - Perform readiness checks and backend forwarding.

This keeps behavior identical between local direct access and public tunnel access.

## Public URL Shape

- Gateway owns a configured apex, e.g. `tunnels.foocorp.dev`.
- Client-visible URL pattern:
  - `<subdomain>.<label>.tunnels.foocorp.dev`
  - `<label>.tunnels.foocorp.dev` (base host, no subdomain)
- `label` must be globally unique while active.

## Registration Model

Agent registration fields:

- `project`: project name from config
- `slug`: resolved worktree slug
- `label`: requested label (default slug)
- `agent_id`: stable local daemon ID
- `owner`: optional user identifier
- `capabilities`: optional feature flags

Gateway constraints:

- Reject duplicate active labels unless same `agent_id` is reconnecting.
- If same label reconnects, newest healthy session replaces old session.

Persistence constraints:

- Registration mutations are written to disk before acking success.
- On startup, persisted labels are loaded as `pending` leases.
- A lease becomes `active` only after the agent reconnects and heartbeats succeed.
- Stale leases are garbage-collected after a configurable expiry window.

## Onboarding and Identity Lifecycle (v1)

Goal: zero browser/OIDC setup for day-to-day team onboarding.

### Invite flow

1. Admin creates invite:
   - `dev-mode gateway invite create`
   - Defaults: single-use, TTL 5 minutes.
2. Teammate logs in with invite code:
   - `dev-mode gateway login <invite-code> [--name <name>]`
   - If `--name` is omitted, default to `$USER`.
3. CLI generates local keypair and CSR.
4. CLI submits `{invite_code, name, csr}` to gateway.
5. Gateway validates invite and issues client cert + chain.
6. CLI stores key/cert in user config directory and writes gateway auth settings.

### Invite constraints

- Single-use by default.
- Default TTL: 5 minutes.
- Invite can optionally be scoped (future) to project/team.
- Consumed invites cannot be reused.

### Certificate lifecycle

- Agent certs are short-lived.
- Daemon auto-renews before expiry (using existing trusted channel/refresh endpoint).
- Gateway supports immediate revocation by:
  - cert serial
  - issued name/device
- Revoked certs are rejected on next connect.

### Identity model

- Identity is operational, not federated (no browser login required in v1).
- Display name defaults to `$USER`, overridable via `--name`.
- Gateway records issuance metadata (`name`, machine identifier, issued_at, expires_at) for auditability.

## Transport Protocol (v1)

- HTTP/2 over mTLS between agent and gateway.
- Gateway validates agent client cert chain against configured CA.
- Agent validates gateway server cert.
- Multiplexed streams are preferred over per-request connections.

Proposed endpoints:

- `POST /_agent/register`
- `POST /_agent/heartbeat`
- `POST /_agent/unregister`
- `CONNECT /_agent/tunnel/{label}` or bidirectional stream endpoint
- `GET /_registry/labels` (already planned; global label registry)
- `POST /_agent/cert/issue` (invite + CSR -> cert chain)
- `POST /_agent/cert/renew` (renew short-lived cert)

Exact wire format can be finalized during implementation; keep request/response streaming compatible with websockets and chunked HTTP bodies.

## Persistent State (v1)

Gateway stores local state files to restore tunnel leases across restarts.

### Storage location

- Default: `~/.local/state/dev-mode/gateway/` for local/dev setups.
- For deployed gateway instances, set `gateway.data_dir` and persist that single directory.
- When `gateway.data_dir` is set, all gateway state/certs/logs/config snapshots live under it.

### Files

- `state/leases.json`: durable label lease records.
- `state/agents.json`: last-seen agent metadata (for observability/recovery hints).
- `logs/audit.log`: append-only registration/renewal/revocation events.
- `pki/`: gateway cert/key material and trust bundles.
- `config/gateway.toml` (optional): gateway runtime config snapshot.

### Lease record shape

- `label`
- `project`
- `slug`
- `agent_id`
- `name` (issued identity/display name)
- `status` (`pending`, `active`, `revoked`, `expired`)
- `created_at`
- `last_seen_at`
- `expires_at` (optional hard lease expiry)

### Startup restore behavior

1. Load `leases.json`.
2. Mark non-revoked/non-expired leases as `pending`.
3. Accept agent reconnects for those labels without requiring re-open from CLI.
4. Promote to `active` on successful register + heartbeat.
5. Route traffic only to `active` leases.

This gives "tunnels restored on startup" semantics while still requiring live agent connectivity before forwarding.

### Write model

- Atomic file replacement (`.tmp` + rename) for JSON snapshots.
- In-memory index remains source for serving; file is durability layer.
- Debounced flush for heartbeat updates to avoid excessive disk churn.

## Failure Behavior

- If no connected agent for label: return `502` with structured error code `gateway_label_unavailable`.
- If stream to agent fails mid-request: return `502` with `gateway_upstream_error`.
- If Basic Auth is enabled and credentials are missing/invalid: return `401` with `WWW-Authenticate: Basic realm="dev-mode gateway"`.
- Gateway logs include request id, host, label, selected agent id, and error code.

## Security

- mTLS required for agent connectivity.
- Registration auth bound to cert identity (plus optional token).
- Optional Basic Auth for public ingress requests:
  - Applied before label lookup/forwarding.
  - Enabled/disabled via user config.
  - Uses constant-time credential comparison.
- Gateway strips/sets forwarding headers to prevent spoofing:
  - Preserve canonical `X-Forwarded-For`
  - Set `X-Forwarded-Proto`, `X-Forwarded-Host`
- Label claim operations are authenticated and audited.

## CLI and Config Surface

Project config:

```toml
[gateway]
url = "https://tunnels.foocorp.dev"
```

User config (future expansion):

```toml
[gateway]
enabled = true
data_dir = "/var/lib/dev-mode-gateway"

[gateway.auth]
enabled = false
username = ""
password = ""
```

Notes:
- `gateway.auth` is global for this gateway instance.
- When enabled, every public request must pass Basic Auth before forwarding.
- Credentials live in user/global config so they are not committed to project config.
- `gateway.data_dir` should be mounted as persistent storage in deployed gateway environments.

CLI behavior:

- `dev-mode tunnel open [slug] [--label <label>]` starts/ensures agent registration.
- `dev-mode tunnel close [slug]` unregisters label.
- `dev-mode tunnels status` shows connection + assigned URLs.

## Observability

Gateway metrics (v1):

- active agents
- active labels
- register/unregister rates
- forwarded request count, latency, error rate by label

Daemon/agent logs:

- register attempts/results
- reconnect backoff
- per-request forwarding failures (sampled if needed)

## Open Implementation Decisions

1. Stream framing choice for HTTP forwarding payloads (raw CONNECT tunnel vs framed HTTP envelope).
2. Backoff/retry policy tuning for flaky networks.
3. Lease expiry default and GC cadence.

## Rollout Plan

1. Implement gateway skeleton with file-backed lease registry (`leases.json`) + health tracking.
2. Implement startup restore (`pending` -> `active` on reconnect).
3. Implement agent register/heartbeat/reconnect loop in daemon.
4. Implement HTTP forwarding path (including websocket upgrades).
5. Wire CLI `tunnel` commands to registration lifecycle.
6. Add integration tests:
   - lease persists across gateway restart
   - reconnect promotes persisted lease to active
   - request through gateway reaches correct local process and auto-starts if needed
