# dev-mode: Dev Experience Spec

## Goals

- Single, multi-project local daemon for worktrees, on-demand servers, reverse proxy, DNS, and tunneling agent.
- Distributed as a single executable with easy cross-compilation (or a truly portable executable).
- No Caddy or dnsmasq; dev-mode provides TLS, DNS, and routing.
- Worktrees are isolated but share project-level services when marked shared.
- Public tunneling supports arbitrary subdomains with label-based routing (no slots).

## CLI

### Global

- `dev-mode --config <path>`: override project config path (default: `.dev-mode.toml`).
- `dev-mode --user-config <path>`: override user config (default: `~/.config/dev-mode/config.toml`).
- `dev-mode --debug`

### Daemon

- `dev-mode daemon start`
- `dev-mode daemon stop`
- `dev-mode daemon status`

### Projects

- `dev-mode projects list`

### Config

- `dev-mode config show`

### Worktrees

- `dev-mode worktree new <path> [branch]` (alias: `dev-mode new <path> [branch]`)
- `dev-mode worktree cleanup [path|slug] [--yes] [--delete-branch]` (alias: `dev-mode cleanup ...`)
- `dev-mode worktree list` (alias: `dev-mode list`)
- `dev-mode worktree status [slug]` (alias: `dev-mode status [slug]`) includes tunnel status for the worktree when available.
- `dev-mode worktree start [slug]` (alias: `dev-mode start [slug]`)
- `dev-mode worktree stop [slug]` (alias: `dev-mode stop [slug]`)

Slugs can be qualified with a project name, e.g. `foocorp/feature-test`.

### Tunnels

- `dev-mode tunnel open [slug] [--label <label>]`
  - Slug defaults to the current worktree when omitted.
  - Label defaults to slug.
- `dev-mode tunnel [slug] [--label <label>]` (alias for `dev-mode tunnel open ...`)
- `dev-mode tunnel close [slug]`
- `dev-mode tunnels status`

### Proxy / DNS / Certs

- `dev-mode dns install`
- `dev-mode dns uninstall`
- `dev-mode cert install`
- `dev-mode gateway`

## Config Files

### Project config (committed)

Path: `$project/.dev-mode.toml`

```toml
[project]
name = "foocorp"         # required
apex_zone = ".localhost"  # required

[gateway]
url = "https://gateway.foocorp.dev"

[services.postgres]
shared = true
command = ["devbox", "services", "start", "postgresql"]
stop_command = ["devbox", "services", "stop", "postgresql"]
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"

[processes.rails]
scope = "worktree"
command = ["rails", "server"]
socket = "${WORKTREE_STATE}/rails.sock"
```

### Project local overrides (not committed)

Path: `$project/.dev-mode.local.toml`

Overrides `.dev-mode.toml` for the current repo.

### User config (trusted worktrees)

Path: `~/.config/dev-mode/config.toml`

```toml
[worktrees."/Users/stephen/code/foocorp/monorepo"]
project = "foocorp"
trusted = true

[gateway]
enabled = false
listen = ":443"
http_listen = ":80"
acme_email = "tech@foocorp.dev"
acme_storage = "~/.config/dev-mode/gateway/acme"
acme_directory = "https://acme-v02.api.letsencrypt.org/directory"
dns_provider = "route53"
```

Trust is **per worktree path**. The daemon prompts on first use and records the trust decision.

## Conventions

- **Global daemon socket:** `~/.config/dev-mode/devd.sock`
- **Global state dir:** `~/.local/state/dev-mode/`
- **Project state dir:** `~/.local/state/dev-mode/<project>/`
- **Worktree state dir:** `~/.local/state/dev-mode/<project>/worktrees/<slug>/`
- **Certs:** `~/.config/dev-mode/certs/`
- **DNS:** `127.0.0.1:15353`
- **Main worktree:** discovered via `git worktree list --porcelain`
- **Gateway ACME storage:** `~/.config/dev-mode/gateway/acme`

## Worktree State Dir Contents

- `rails.sock`
- `webpack.sock`
- `<process>.log`
- `pids.json` (optional)
- `status.json` (optional)

## Tunnel Labels

- Public URLs: `{subdomain}.{label}.foocorp.dev`
- Label defaults to worktree slug; override with `--label`.
- One label per worktree.
- Labels must be valid DNS labels (`[a-z0-9-]{1,63}`, not starting/ending with `-`).
- Labels are **globally unique** across the gateway.

## Gateway Protocol (HTTP/2 over mTLS)

### Register

`POST /_agent/register`

Headers:
- `x-project`, `x-agent-id`, `x-owner`, `x-label`, `x-slug`

Body:
```json
{"project":"foocorp","label":"feature-auth-jim","slug":"feature-auth","owner":"stephen"}
```

### Stream

`POST /_agent/stream`

Headers:
- `x-session-id`, `x-project`, `x-label`, `x-slug`, `x-method`, `x-path`, `x-host`, `x-scheme`

Body: raw request body. Response returns `x-status` and response headers + body on the same stream.

### Ping

`GET /_agent/ping`

### Registry (global)

`GET /_registry/labels`

```json
{
  "labels": [
    {
      "label": "feature-auth-jim",
      "project": "foocorp",
      "slug": "feature-auth",
      "owner": "stephen",
      "hostname": "devbox-123",
      "connected": true,
      "last_seen": "2026-01-31T20:01:12Z"
    }
  ]
}
```

`GET /_registry/labels/{label}`

Conflict response on registration:
```json
{"error":"label_in_use","label":"feature-auth-jim","project":"foocorp","owner":"stephen"}
```

## Routing Behavior

- Gateway routes `*.{label}.foocorp.dev` to the agent registered for `{label}`.
- dev-mode rewrites `app.{label}.foocorp.dev` → `app.{slug}.localhost` before proxying to sockets.
- Response header rewrites include `Location` and `Set-Cookie Domain`.
- Optional HTML body rewrite is controlled by `proxy.rewrite_html`.

## Gateway TLS (ACME)

- `dev-mode gateway` runs in the foreground and is intended to be managed by systemd.
- Gateway terminates TLS for `*.foocorp.dev`.
- Certificates are obtained via ACME DNS-01 (Route53) to support wildcards.
- The gateway should request:
  - A wildcard for `*.foocorp.dev`
  - On-demand wildcard for `*.{label}.foocorp.dev` when needed by agents
- ACME state is stored in `~/.config/dev-mode/gateway/acme`.
