# Gateway Hooks Proposal

## Goal

Allow projects to define hook commands that run when gateway connectivity changes, so teams can integrate notifications (for example Slack) and lightweight automation.

## Use Cases

- Notify a channel when a worktree is shared and becomes reachable.
- Notify when a share disconnects or fails.
- Emit machine-readable events to local scripts for dashboards.
- Trigger short follow-up actions (audit logs, ephemeral access tracking, etc.).

## Proposed Hook Events

Add a new optional `[gateway.hooks]` block in project config:

```toml
[gateway.hooks]
on_connect = "bin/gateway-connected"
on_disconnect = "bin/gateway-disconnected"
on_error = "bin/gateway-error"
```

Event semantics:

- `on_connect`: runs after a tunnel/agent transitions to connected.
- `on_disconnect`: runs after a tunnel/agent transitions away from connected.
- `on_error`: runs when connection establishment or steady-state operation reports an error.

## Execution Model

- Hooks are best-effort and non-blocking for gateway runtime.
- Hook execution must not block tunnel health loops.
- Hook output should be prefixed and written to daemon logs.
- Add per-hook timeout (for example 10-30s); timeout counts as failure.

## Working Directory

Run gateway hooks with working directory set to the target worktree path when available.
If unavailable, fall back to the project main worktree path.

## Hook Environment

Proposed minimum environment variables:

- `DEV_EVENT` (`connect`, `disconnect`, `error`)
- `DEV_PROJECT`
- `DEV_WORKTREE_SLUG`
- `DEV_WORKTREE_DNS_NAME`
- `DEV_GATEWAY_URL`
- `DEV_GATEWAY_LABEL`
- `DEV_GATEWAY_PUBLIC_HOST`
- `DEV_GATEWAY_STATUS`
- `DEV_GATEWAY_ERROR` (set for `error` events)
- `DEV_GATEWAY_TIMESTAMP` (RFC3339)

## Deduping / Flap Control

To avoid notification spam from reconnect loops:

- Only fire `on_connect` on state change into `connected`.
- Only fire `on_disconnect` on state change out of `connected`.
- Optionally add debounce window for repeated identical `on_error` events.

## Failure Behavior

- Hook failure should not change gateway/tunnel state.
- Failures should be logged with event context and exit code.
- Optionally expose an in-memory counter for failed hook executions in daemon status.

## Security Considerations

- Hooks execute local shell commands; treat config as trusted code.
- Avoid injecting secrets in plaintext logs.
- Sanitize any remote-derived fields before exporting env vars.

## Observability

- Log start/end, duration, exit status, and event key fields.
- Consider adding `dev daemon status` summary for recent gateway hook runs.

## Open Questions

- Should hooks be project-level only, or also daemon-level defaults?
- Should we support multiple commands per event (array) or keep single command for simplicity?
- Do we need retry semantics for `on_error` failures, or leave retries to user scripts?
- Should `on_connect` fire for every reconnect, or only first connect per daemon session?
