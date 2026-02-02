# Config Execution Trust Model (Future RFC)

Status: **proposed only**. This is not implemented yet.

This RFC describes an optional future safety model for executing command-bearing config fields.

## Problem

`.dev-mode.toml` can execute arbitrary commands with the user's privileges (process commands, wrappers, hooks). A malicious repo can abuse that.

## Goals

- Require explicit approval before executing command-bearing config from a worktree.
- Make approvals reviewable (show exactly what will execute).
- Re-prompt when command-bearing content changes.
- Keep day-to-day UX low-friction.

## Non-Goals

- Sandboxing command execution.
- Per-command allowlists.
- Team-synced trust policies in v1.

## Scope of Command-Bearing Fields

Hash/input set must include all executable fields in current config model:

- `[process.<name>].command`
- `[process.<name>].wrapper` (if set)
- `[commands].wrapper` (project-wide wrapper)
- `[hooks].post_create`
- `[hooks].pre_cleanup`
- `[hooks].pre_start`
- `[hooks].post_start`
- `[hooks].pre_stop`
- `[hooks].post_stop`

Notes:

- `process.<name>.env`, `port`, `proxy`, `health`, `startup_timeout`, `singleton` are not directly executable and are excluded.
- Hashing should be deterministic by field name and process key order.

## Trust Identity and Storage (Proposed)

Trust is granted per resolved config path + command hash:

```json
{
  "config_path": "/abs/path/to/.dev-mode.toml",
  "commands_hash": "sha256:...",
  "approved_at": "2026-02-02T12:34:56Z",
  "approved_by": "local-user"
}
```

Proposed store:

- `~/.config/dev-mode/trust.json`

This is intentionally separate from normal user settings.

## Daemon API Integration (Current Endpoint Surface)

If trust gating is enabled later, the daemon should apply it to current endpoints:

- `POST /processes/start`
- `POST /processes/stop` (only for executable hooks, if any)
- `POST /worktrees/start`
- Proxy auto-start path before starting a stopped target process

### Proposed Error Contract

```json
{
  "code": "trust_required",
  "error": "unapproved command-bearing config",
  "challenge": {
    "config_path": "/abs/path/to/.dev-mode.toml",
    "commands_hash": "sha256:...",
    "entries": [
      {"scope":"process:web","field":"command","value":"npm run dev"},
      {"scope":"hooks","field":"pre_start","value":"bin/pre-start"}
    ]
  }
}
```

For proxy-triggered auto-start, return HTTP 503 with `code=trust_required`.

## Proposed Approval Endpoint

- `POST /trust/approve`

Request:

```json
{
  "config_path": "/abs/path/to/.dev-mode.toml",
  "commands_hash": "sha256:..."
}
```

Response:

```json
{"status":"ok"}
```

## CLI UX (Proposed)

When receiving `trust_required`:

1. Show command-bearing entries.
2. Allow approve/abort.
3. On approve, call `POST /trust/approve`.
4. Retry original request once.

## Security Considerations

- Resolve symlinks before path-based trust lookup.
- Require `0600` permissions for trust store.
- Re-validate hash at execution time to avoid TOCTOU between prompt and run.

## Implementation Checklist (Future)

- Add trust store module and deterministic hasher for current config schema.
- Wire trust checks into process/worktree start paths and auto-start.
- Add CLI challenge handling + approval flow.
- Add tests for:
  - first-run prompt behavior
  - hash-change re-prompt
  - proxy auto-start 503 behavior
  - symlink/path spoof protection

## Decision (Rejected for Now)

This RFC is rejected for now due to implementation complexity and UX/API surface area.

We may revisit this later with a simpler approach.

For now, do not execute `dev-mode` in an untrusted repository without reviewing `.dev-mode.toml` (and any local overrides) first.
