# dev

`dev` is a CLI + daemon that runs project processes across git worktrees and exposes them to the local network and, via a gateway, to the public internet.

## Language

### Gateway exposure

**Service**:
A process defined by a `[process.<name>]` entry and made reachable through the gateway by listing it in `[gateway.expose]`.
_Avoid_: app, upstream

**Tunnel**:
A single connection from the daemon to the gateway that exposes one **Worktree**'s **Services** to the public internet. Identified by a **Label**.
_Avoid_: share (verb form `dev share` is fine; the noun is a **Tunnel**)

**Label**:
The subdomain segment that identifies a **Tunnel** at the gateway, e.g. `<label>.<zone>`. One **Label** maps to exactly one **Worktree**.

**Share auth**:
HTTP Basic Auth enforced on inbound public **Tunnel** requests. Configured per-project via `[gateway.auth]`, overridable per-tunnel with `--auth` / `--no-auth`. Required for every request by default; an individual **Service** may opt out (`[gateway.expose.<service>] auth = false`), making just that service public.
_Avoid_: gateway auth (ambiguous — see Flagged ambiguities)

**Expose mode**:
Per-**Service** strategy for gateway traffic: `reverse_proxy` (default), `rewrite` (translate hostnames in headers/body), or `disable` (not exposed at all).

## Relationships

- A **Tunnel** exposes one **Worktree**'s **Services** under one **Label**.
- **Services** on a **Tunnel** share one **Share auth** credential pair, but a **Service** may individually opt out of auth.
- A **Service**'s **Expose mode** is independent of **Share auth**; `disable` removes the **Service** from the gateway, whereas `auth = false` keeps it exposed but unauthenticated.

## Flagged ambiguities

- "gateway auth" was used for two distinct mechanisms: (1) **Share auth** — public Basic Auth on tunnel requests; (2) the daemon↔gateway mTLS agent connection (`DaemonGatewayAuth`). These are unrelated. "Per-service auth" refers only to **Share auth**.
