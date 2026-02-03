# dev-mode Usage

See `docs/config.md` for configuration options and examples. See `docs/environment.md` for runtime/hook environments. See `docs/hooks.md` for hook behavior. See `docs/logging.md` for log locations.

## Global Options

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--daemon-config <path>`: override daemon config (default: `~/.config/dev-mode/daemon.toml`).
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
Lists projects, their main worktree path, and known worktrees per project.

## Worktree Lifecycle

### `dev-mode worktree add <slug> [branch]`
Creates a managed worktree under daemon `worktree_dir`. Accepts `slug` or `project:slug` format.
When only `slug` is provided, project is inferred from the current working directory.
Slugs can contain slashes (e.g., `feature/my-branch`).

### `dev-mode worktree cleanup [slug] [--delete-branch] [--dry-run] [--force]`
Removes a managed worktree. Accepts `slug` or `project:slug` format.
If omitted, dev-mode targets the current worktree from cwd. When `slug` is provided without project, project is inferred from cwd.

### `dev-mode worktree list`
Lists managed worktrees with path, branch, and status flags.

## Config

### `dev-mode init`
Initializes a `.dev-mode.toml` project config in the current directory.
Defaults `project.name` to `<owner>/<repo>` when the git `origin` remote matches that format; otherwise uses the directory basename.

### `dev-mode config show`
Shows the effective project config (with local override applied) and daemon config.
Use `--output json` for machine-readable output.

## Documentation

### `dev-mode docs <basename>`
Shows embedded markdown from `docs/<basename>.md` (for example, `dev-mode docs config`).
Output is piped through `DEV_MODE_PAGER`, then `bat` (if installed), then `PAGER`, then stdout.

## Process Identifiers

Many commands accept process identifiers in these forms:

- `process` (current worktree inferred from cwd)
- `slug:process`
- `project:slug:process`
- `slug:*` (all processes in that worktree)

Slugs can contain slashes (e.g., `feature/my-branch:rails`).

If no process identifiers are provided to `start`/`stop`/`restart`, dev-mode targets all processes in the current worktree.

See `docs/identifiers.md` for full details on identifier format and resolution.

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

### `dev-mode attach <process|slug:process|project:slug:process>`
Attaches your terminal to a process PTY. Uses the current worktree when only `process` is provided.

## Tunnels

### `dev-mode share [slug] [--label <label>] [--gateway-url <url>]`
Shares a worktree through the gateway. Slug defaults to the current worktree when omitted; label defaults to slug.
Gateway URL comes from project `gateway.url` unless overridden with `--gateway-url`.

### `dev-mode unshare [slug] [--label <label>] [--gateway-url <url>]`
Stops sharing the worktree.

## DNS

### `dev-mode dns install`
Installs the `localhost` resolver to point at the dev-mode DNS server.

### `dev-mode dns uninstall`
Removes the `localhost` resolver.

## Certs

### `dev-mode cert install`
Creates a local CA and installs trust (mkcert-style). Generates local certs for `*.localhost`.

### `dev-mode cert export`
Writes the local CA certificate PEM (`ca.pem`) to stdout.

## Install

### `dev-mode install`
Installs DNS, certs, and proxy privileges (requires sudo).


## Gateway

### `dev-mode gateway run`
Runs the gateway in the foreground.

### `dev-mode gateway invite create [--ttl 5m] [--uses 1]`
Creates an invite code for onboarding a new agent identity.

### `dev-mode gateway login <invite-code> [--name <name>] [--gateway-url <url>]`
Exchanges an invite code for client certificate credentials and stores them locally.
