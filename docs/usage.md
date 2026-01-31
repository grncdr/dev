# dev-mode Usage

See `docs/config.md` for configuration options and examples.

## Global Options

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--user-config <path>`: override user config (default: `~/.config/dev-mode/config.toml`).
- `--debug`

## Daemon

### `dev-mode daemon start`
Starts the single global daemon (proxy, DNS, certs, shared services, worktree supervisor, tunnel agent).

### `dev-mode daemon stop`
Stops the global daemon.

### `dev-mode daemon status`
Shows daemon health plus proxy/DNS/cert status and shared service state.

## Projects

### `dev-mode projects list`
Lists all trusted projects, their main worktree path, and known worktrees per project.

## Config

### `dev-mode config show`
Shows the effective project config (with local override applied) and user config.
Use `--output json` for machine-readable output.

## Worktrees

### `dev-mode worktree new <path> [branch]`
Creates a new worktree and initializes per-worktree state.

Alias: `dev-mode new <path> [branch]`

### `dev-mode worktree cleanup [path|slug] [--yes] [--delete-branch]`
Stops the worktree (if running), removes state and data, and deletes the worktree directory.

Alias: `dev-mode cleanup [path|slug] [--yes] [--delete-branch]`

### `dev-mode worktree list`
Lists configured worktrees.

Alias: `dev-mode list`

### `dev-mode worktree status [slug]`
Shows worktree status (processes, sockets, idle time). Includes tunnel status when available.

Alias: `dev-mode status [slug]`

### `dev-mode worktree start [slug]`
Starts worktree processes on demand.

Alias: `dev-mode start [slug]`

### `dev-mode worktree stop [slug]`
Stops worktree processes.

Alias: `dev-mode stop [slug]`

Slugs can be qualified with a project name, e.g. `foocorp/feature-test`.

## Tunnels

### `dev-mode tunnel open [slug] [--label <label>]`
Opens a tunnel for the worktree. Slug defaults to the current worktree when omitted; label defaults to slug.

Alias: `dev-mode tunnel [slug] [--label <label>]`

### `dev-mode tunnel close [slug]`
Closes the tunnel for the worktree.

### `dev-mode tunnels status`
Shows all active tunnel labels and owners from the gateway registry.

## DNS

### `dev-mode dns install`
Installs the `localhost` resolver to point at the dev-mode DNS server.

### `dev-mode dns uninstall`
Removes the `localhost` resolver.

## Certs

### `dev-mode cert install`
Creates a local CA and installs trust (mkcert-style). Generates local certs for `*.localhost`.

## Gateway

### `dev-mode gateway`
Runs the gateway in the foreground (intended for systemd).
