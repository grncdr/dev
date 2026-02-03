# dev-mode: Dev Experience

High-level product goals and UX surface for `dev-mode`.

## Goals

- Single local daemon for process orchestration, local HTTPS proxying, and gateway agent tunneling.
- Distributed as a single executable with straightforward cross-compilation.
- Process-centric config with worktree-aware defaults.
- Keep the local proxy as the source of truth for request routing; gateway forwards only.

## CLI Surface

### Global

- `dev-mode --config <path>`
- `dev-mode --daemon-config <path>`
- `dev-mode --debug`
- Commands that talk to the daemon auto-start it by default.
- Exceptions: `dev-mode daemon status` and `dev-mode daemon stop`.

### Daemon

- `dev-mode daemon run`
- `dev-mode daemon start`
- `dev-mode daemon stop`
- `dev-mode daemon status`
- `dev-mode daemon restart`

### Project and Config

- `dev-mode projects list`
- `dev-mode init`
- `dev-mode config show`
- `dev-mode config show --output json`

### Process Lifecycle

- `dev-mode start [process_identifier ...]`
- `dev-mode stop [process_identifier ...]`
- `dev-mode restart [process_identifier ...]`
- `dev-mode status [process_identifier ...]`
- `dev-mode attach <process|slug:process|project:slug:process>`

Process identifiers use colon separators (e.g., `project:slug:process`). Slugs can contain slashes (e.g., `feature/my-branch`).
See `docs/identifiers.md` for full format documentation.
When omitted, `start|stop|restart|status` operate on all processes in the current worktree.

### Sharing / Gateway

- `dev-mode share [slug] [--label <label>]`
- `dev-mode unshare [slug]`
- `dev-mode gateway run`
- `dev-mode gateway invite create`
- `dev-mode gateway login <invite-code> [--name <name>] [--gateway-url <url>]`

### Local Machine Setup

- `dev-mode dns install`
- `dev-mode dns uninstall`
- `dev-mode cert install`
- `dev-mode install`

## Config Model

- Project config: `.dev-mode.toml`
- Local overrides: `.dev-mode.local.toml`
- Daemon config: `~/.config/dev-mode/daemon.toml`
- Process table is `[process.<name>]` (singular section name).

See:

- `docs/config.md` for user-facing config reference.
- `docs/design/routing.md` for proxy matcher semantics and request precedence.
- `docs/design/gateway.md` for gateway/agent protocol and lifecycle.

## Conventions

- Daemon socket: `~/.config/dev-mode/devd.sock`
- State root: `~/.local/state/dev-mode/` (override via `DEV_MODE_STATE_DIR`)
- Logs: `<state_root>/logs/`
  - Daemon: `<state_root>/logs/daemon.log`
  - Processes: `<state_root>/logs/<project>/<slug>/<process>.log`
- Certificates: `~/.config/dev-mode/certs/`
- Proxy apex defaults to `.localhost` via daemon config `local-proxy.apex_zone`.
