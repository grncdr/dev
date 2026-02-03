# Gateway Dashboard Proposal

## Goal

Add a minimal web UI at `/dashboard` on the public gateway that shows all known public URLs and their current connectivity state.

## Scope

- New HTTP route: `GET /dashboard`
- Server-rendered HTML (no SPA required)
- Read-only view of routing/tunnel status
- Protected with the same gateway basic auth used for other protected public URLs

## Requirements

- `/dashboard` must require gateway basic auth when auth is enabled.
- Dashboard lists all known public URLs, including wildcard/base routes where relevant.
- For each URL, show status such as:
  - connected (active agent/tunnel)
  - disconnected (known route but no active connection)
  - error (known connection problem)
- Include key metadata per row:
  - project
  - slug/label
  - public host/url
  - current status
  - last error (if any)
  - last updated time

## Data Sources

Potential source of truth is existing gateway/tunnel state in memory/store:

- active tunnel registrations
- known labels/public hosts
- last known status/error

If only active sessions are currently stored, add lightweight persistence/indexing so disconnected-but-known entries can still be shown.

## UX

- Keep it simple and fast:
  - sortable table
  - status badges (connected/disconnected/error)
  - optional auto-refresh (e.g. 5-10s)
- Desktop/mobile readable.
- No write actions in v1.

## Security

- Apply the same auth middleware/path checks used for existing public gateway routes.
- Avoid exposing secrets or raw credentials in rendered output.
- Ensure no sensitive internal addresses are leaked beyond intended fields.

## Failure Behavior

- If status backend is unavailable, render dashboard with clear degraded-state banner.
- Route should still return `200` with partial data when possible.

## Observability

- Add request logging for `/dashboard`.
- Optionally expose dashboard render errors in existing gateway logs.

## Open Questions

- Should `/dashboard` be HTML-only, or include a JSON endpoint (`/dashboard.json`) for tooling?
- Do we need filtering (by project/label/status) in v1, or defer?
- How long should disconnected entries be retained?
- Should status update be polling only, or eventually SSE/WebSocket?
