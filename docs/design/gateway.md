# Gateway Design

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
- **Agent**: code running in `dev daemon` that maintains outbound tunnel connections.
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

- Gateway requires `gateway.dns_zone` and owns that configured apex, e.g. `tunnels.foocorp.dev`.
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
   - Bootstrap: `dev gateway init` (on gateway host)
   - Ongoing: `dev gateway invite` (from any connected agent)
   - Defaults: single-use, TTL 5 minutes.
2. Teammate logs in with invite code:
   - `dev gateway login <invite-code> [--name <name>] --gateway-url <url>`
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
- **Invite codes are intentionally not bound to requester identity** (IP, email, etc.). This supports flexible team workflows where an admin generates an invite and shares it via any channel (Slack, email, in-person). The short TTL and single-use defaults provide sufficient protection against abuse.

### Certificate lifecycle

- Agent certs are short-lived.
- Daemon auto-renews before expiry (using an authenticated refresh endpoint).
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
- `POST /_agent/invites/create` (agent-authenticated invite creation)
- `POST /_agent/cert/issue` (invite + CSR -> cert chain)
- `POST /_agent/cert/renew` (renew short-lived cert)

Exact wire format can be finalized during implementation; keep request/response streaming compatible with websockets and chunked HTTP bodies.

## Persistent State (v1)

Gateway stores local state files to restore tunnel leases across restarts.

### Storage location

- Default: `~/.local/state/dev/gateway/` (override via `DEV_STATE_DIR`)
- For deployed gateway instances, set `DEV_STATE_DIR` and persist that directory.
- All gateway state/certs/logs/config snapshots live under `<state_dir>/gateway/`.

### Files

- `state/leases.json`: durable label lease records.
- `state/agents.json`: last-seen agent metadata (for observability/recovery hints).
- `logs/audit.log`: append-only registration/renewal/revocation events.
- `pki/`: gateway cert/key material and CA bundles.
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
- If Basic Auth is enabled and credentials are missing/invalid: return `401` with `WWW-Authenticate: Basic realm="dev gateway"`.
- ACME provisioning applies backoff on failures and honors Let’s Encrypt `retry after` hints to avoid repeated failed authorizations.
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
- Gateway TLS certificates are provisioned via ACME (Let’s Encrypt) using DNS-01 with Route53:
  - startup: `*.{dns_zone}`
  - on label registration: `*.{label}.{dns_zone}`

## CLI and Config Surface

Project config:

```toml
[gateway]
url = "https://tunnels.foocorp.dev"
```

Daemon config (future expansion):

```toml
[gateway]
enabled = true
dns_zone = "tunnels.foocorp.dev"
hostname = "gw.foocorp.dev"
acme_resolvers = ["1.1.1.1"]

[gateway.auth]
enabled = false
username = ""
password = ""

[gateway.route53]
enabled = true
hosted_zone_id = "Z1234567890"
ttl = 60
```

Notes:
- `gateway.auth` is global for this gateway instance.
- When enabled, every public request must pass Basic Auth before forwarding.
- Credentials live in daemon config so they are not committed to project config.
- `gateway.dns_zone` is required; the gateway refuses to start without it.
- `gateway.hostname` is the DNS CNAME target used for Route53 label records.
- `gateway.acme_resolvers` sets recursive resolvers for DNS-01 propagation checks (default `1.1.1.1`).
- If `gateway.route53.enabled = true`, gateway register/unregister calls sync Route53 DNS records for labels.

CLI behavior:

- `dev share [slug] [--label <label>]` starts/ensures agent registration.
- `dev unshare [slug] [--label <label>]` unregisters label.
- `dev status` shows sharing connection state in the Gateway section.

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

## Current Status

Implemented baseline:

- Gateway run command, persistent state, invite creation, and login/cert issue flow.
- Agent registration/heartbeat/unregistration with reconnect behavior.
- Label registry endpoint and gateway-provided public hostname responses.
- Optional Basic Auth on public ingress.
- Route53 DNS record sync for label lifecycle.
- ACME wildcard provisioning hooks with backoff-aware failure handling.

## Remaining Work

1. Production hardening for retry/backoff tuning and observability depth.
2. Expanded operational tooling (admin UX, cert revocation ergonomics, deeper metrics).
3. Additional end-to-end coverage under adverse network and restart conditions.
