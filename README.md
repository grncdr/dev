# dev-mode

`dev-mode` is a local development orchestration tool for multi-worktree projects.

It provides a single CLI + daemon that can:

- start/stop/restart project processes by worktree
- route local HTTPS traffic to processes with per-process proxy matchers
- auto-start proxied processes on demand
- manage local DNS/cert setup for `*.localhost` style development
- run a gateway + agent tunnel model for public label-based access

The project is implemented in Go and aims to be distributed as a single executable.

## Getting started

Build/install locally:

```bash
go install ./cmd/dev-mode
```

Run tests:

```bash
go test ./...
```

## Key docs

- Usage: `docs/usage.md`
- Config reference: `docs/config.md`
- Routing design: `docs/design/routing.md`
- Gateway design: `docs/design/gateway.md`
- Logging: `docs/logging.md`
