# dev: Dev Experience

High-level product goals and UX surface for `dev`.

## Goals

- Single local daemon for process orchestration, local HTTPS proxying, and gateway agent tunneling.
- Distributed as a single executable with straightforward cross-compilation.
- Process-centric config with worktree-aware defaults.
- Keep the local proxy as the source of truth for request routing; gateway forwards only.

## CLI Surface

### Global

- `dev --config <path>`
- `dev --daemon-config <path>`
- `dev --debug`
- Commands that talk to the daemon auto-start it by default.
- Exceptions: `dev daemon status` and `dev daemon stop`.

### Daemon

- `dev daemon run`
- `dev daemon start`
- `dev daemon stop`
- `dev daemon status`
- `dev daemon restart`

### Project and Config

- `dev projects list`
- `dev init`
- `dev config show`
- `dev config show --output json`

### Process Lifecycle

- `dev start [process_identifier ...]`
- `dev stop [process_identifier ...]`
- `dev restart [process_identifier ...]`
- `dev status [process_identifier ...]`
- `dev attach <process|slug:process|project:slug:process>`

Process identifiers use colon separators (e.g., `project:slug:process`). Slugs can contain slashes (e.g., `feature/my-branch`).
See `docs/identifiers.md` for full format documentation.
When omitted, `start|stop|restart|status` operate on all processes in the current worktree.

### Sharing / Gateway

- `dev share [slug] [--label <label>]`
- `dev unshare [slug]`
- `dev gateway run`
- `dev gateway init`
- `dev gateway invite`
- `dev gateway login <invite-code> [--name <name>] [--gateway-url <url>]`

### Local Machine Setup

- `dev dns install`
- `dev dns uninstall`
- `dev cert install`
- `dev install`

## Config Model

- Project config: `.dev.toml`
- Local overrides: `.dev.local.toml`
- Daemon config: `~/.config/dev/daemon.toml`
- Process table is `[process.<name>]` (singular section name).

See:

- `docs/config.md` for user-facing config reference.
- `docs/design/routing.md` for proxy matcher semantics and request precedence.
- `docs/design/gateway.md` for gateway/agent protocol and lifecycle.

## Conventions

- Daemon socket: `~/.config/dev/devd.sock`
- State root: `~/.local/state/dev/` (override via `DEV_STATE_DIR`)
- Logs: `<state_root>/logs/`
  - Daemon: `<state_root>/logs/daemon.log`
  - Processes: `<state_root>/logs/<project>/<slug>/<process>.log`
- Certificates: `~/.config/dev/certs/`
- Proxy apex defaults to `.localhost` via daemon config `local-proxy.apex_zone`.
