# dev-mode Usage

See `docs/config.md` for configuration options and examples. See `docs/logging.md` for log locations.

## Global Options

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--user-config <path>`: override user config (default: `~/.config/dev-mode/config.toml`).
- `--debug`

## Daemon

### `dev-mode daemon start`
Starts the single global daemon (proxy, DNS, certs, shared services, worktree supervisor, tunnel agent).
If the daemon was previously shut down cleanly, it restores previously running worktrees/services on startup.

### `dev-mode daemon stop`
Stops the global daemon.
On clean shutdown, the daemon saves currently running worktrees/services and restores them on the next start.

### `dev-mode daemon status`
Shows daemon health plus proxy/DNS/cert status and shared service state.

### `dev-mode daemon restart`
Restarts the daemon.

## Projects

### `dev-mode projects list`
Lists all trusted projects, their main worktree path, and known worktrees per project.

## Config

### `dev-mode init`
Initializes a `.dev-mode.toml` project config in the current directory.
Defaults `project.name` to the directory basename.

### `dev-mode config show`
Shows the effective project config (with local override applied) and user config.
Use `--output json` for machine-readable output.

## Process Identifiers

Many commands accept process identifiers in these forms:

- `process` (current worktree inferred from cwd)
- `worktree:process`
- `project/worktree:process`
- `project/worktree:*` (all processes in that worktree)

If no process identifiers are provided to `start`/`stop`/`restart`, dev-mode targets all processes in the current worktree.

## Process Control

### `dev-mode status [process_identifier...]`
Shows process status. With no args, shows all processes in the current worktree.

### `dev-mode start [process_identifier...]`
Starts processes. With no args, starts all processes in the current worktree.

### `dev-mode stop [process_identifier...]`
Stops processes. With no args, stops all processes in the current worktree.

### `dev-mode restart [process_identifier...]`
Restarts processes. With no args, restarts all processes in the current worktree.

## Attach

### `dev-mode attach <process|slug:process|project/slug:process>`
Attaches your terminal to a process PTY. Uses the current worktree when only `process` is provided.

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

## Install

### `dev-mode install`
Installs DNS, certs, and proxy privileges (requires sudo).


## Gateway

### `dev-mode gateway`
Runs the gateway in the foreground.
