# dev-mode: Dev Experience Spec

## Goals

- Single, multi-project local daemon for worktrees, on-demand servers, reverse proxy, DNS, and tunneling agent.
- Distributed as a single executable with easy cross-compilation (or a truly portable executable).
- No Caddy or dnsmasq; dev-mode provides TLS, DNS, and routing.
- Worktrees are isolated but share project-level services.
- Public tunneling supports arbitrary subdomains with label-based routing (no slots).

## CLI

### Global

- `dev-mode --config <path>`: override project config path (default: `.dev-mode.toml`).
- `dev-mode --user-config <path>`: override user config (default: `~/.config/dev-mode/config.toml`).
- `dev-mode --debug`
- Most commands that require the daemon will auto-start it if not running.
- Exceptions: `dev-mode daemon status` never auto-starts; `dev-mode daemon stop` does not auto-start.

### Daemon

- `dev-mode daemon start`
- `dev-mode daemon stop`
- `dev-mode daemon status`
- `dev-mode daemon restart`
- Clean daemon shutdown saves currently running worktrees/services and restores them on the next daemon start.

### Projects

- `dev-mode projects list`

### Config

- `dev-mode config show`
- `dev-mode init`
  - Creates `.dev-mode.toml` in the current directory if missing.
  - Defaults `project.name` to the basename of the current directory.

### Worktrees and Processes

- `dev-mode status [process_identifier...]` includes status for selected processes/worktrees.
- `dev-mode start [process_identifier...]`
- `dev-mode stop [process_identifier...]`
- `dev-mode restart [process_identifier...]`
- `dev-mode attach <process|slug:process|project/slug:process>`

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
- `dev-mode install`
- `dev-mode gateway`

### Proxy routing (local)

- Proxy listens on `0.0.0.0:80` and `0.0.0.0:443` by default and redirects HTTP → HTTPS.
- Override with `proxy.listen_http` / `proxy.listen_https` in user config or `DEV_MODE_PROXY_LISTEN_HTTP` / `DEV_MODE_PROXY_LISTEN_HTTPS` (set to `off` to disable).
- Host format: `<subdomain>.<apex_zone>` routes using `proxy.subdomains` rules.
- Processes define a `port` mode in config (e.g. `port = "unix"` or `port = "random"`).
- The daemon injects env vars for started processes:
  - `DEV_MODE_SOCKET` and `DEV_MODE_SOCKET_<PROCESS>` (uppercase, non-alnum → `_`)
  - `WORKTREE_STATE`, `WORKTREE_PATH`, `MAIN_WORKTREE`, `PROJECT_NAME`, `WORKTREE_SLUG`
- Subdomain routing uses `proxy.subdomains` rules; path matches are prefix-based with longest-match wins. `prefix` matches segment boundaries; use `/foo*` for raw prefixes.
- Unqualified `backend` names are resolved against the worktree slug matching the subdomain.
- `project:<name>` routes to a shared process in the main worktree; `local:<name>` routes to a configured local backend.

## Config Files

### Project config (committed)

Path: `$project/.dev-mode.toml`

```toml
[project]
name = "Foo Corp"         # required

[gateway]
url = "https://gateway.foocorp.dev"

[processes.postgres]
singleton = true
command = "devbox services start postgresql"
stop_command = "devbox services stop postgresql"
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"

[commands]
wrapper = "devbox run $COMMAND"

[processes.rails]
singleton = false
command = "rails server"
port = "unix"
wrapper = "bundle exec $COMMAND"

[processes.rails.env]
RAILS_ENV = "development"
PORT = "3000"

[proxy.subdomains."minio"]
backend = "project:minio"

[proxy.subdomains."*"]
backend = "rails"

[proxy.subdomains."*".paths."/blah"]
backend = "node"
match = "prefix"

[proxy.subdomains."*".paths."/raw*"]
backend = "node"
match = "prefix"

[proxy.locals.postgres]
network = "tcp"
address = "127.0.0.1:5432"

[hooks]
post_create = "bin/setup-worktree"
pre_cleanup = "bin/teardown-worktree"
pre_start = "bin/pre-start"
post_start = "bin/post-start"
pre_stop = "bin/pre-stop"
post_stop = "bin/post-stop"
```

Notes:
- All runnable units live under `processes.*`; singleton services use `singleton = true`.
- Hooks run in the worktree directory: `post_create` after worktree creation, `pre_cleanup` before cleanup, and `pre/post start/stop` around `worktree start/stop`.
- `port` controls how the process is reached by the proxy:
  - `"unix"` uses a unix socket at `${WORKTREE_STATE}/<process>.sock`.
  - `"random"` allocates a high port and sets `PORT` / `DEV_MODE_PORT` env vars.
  - An integer uses a fixed localhost TCP port.
- `commands.wrapper` wraps any command execution; use `$COMMAND` to inject the command tokens.
- Each process can override with `processes.<name>.wrapper`.

### Project local overrides (not committed)

Path: `$project/.dev-mode.local.toml`

Overrides `.dev-mode.toml` for the current repo.

### User config (trusted worktrees)

Path: `~/.config/dev-mode/config.toml`

```toml
[worktrees."/abs/path/to/foocorp/monorepo"]
project = "Foo Corp"
trusted = true

[gateway]
enabled = false
data_dir = ""
listen = ":443"
http_listen = ":80"
acme_email = "tech@foocorp.dev"
acme_storage = "~/.config/dev-mode/gateway/acme"
acme_directory = "https://acme-v02.api.letsencrypt.org/directory"
dns_provider = "route53"

[gateway.auth]
enabled = false
username = ""
password = ""

[proxy]
apex_zone = ".localhost"
listen_http = "0.0.0.0:80"
listen_https = "0.0.0.0:443"
allow = "loopback"
```

`gateway.auth` is a user/global setting and applies to all public gateway requests for that user instance.
`gateway.data_dir` is the gateway persistence root for deployed gateway instances.

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
{"project":"Foo Corp","label":"feature-auth-jim","slug":"feature-auth","owner":"stephen"}
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
      "project": "Foo Corp",
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
{"error":"label_in_use","label":"feature-auth-jim","project":"Foo Corp","owner":"stephen"}
```

## Routing Behavior

- Gateway routes `*.{label}.foocorp.dev` to the agent registered for `{label}`.
- dev-mode rewrites `app.{label}.foocorp.dev` → `app.{slug}.localhost` before proxying to sockets.
- Response header rewrites include `Location` and `Set-Cookie Domain`.
- Optional HTML body rewrite is controlled by `proxy.rewrite_html`.

## Gateway TLS (ACME)

- `dev-mode gateway` runs in the foreground.
- Gateway terminates TLS for `*.foocorp.dev`.
- Certificates are obtained via ACME DNS-01 (Route53) to support wildcards.
- The gateway should request:
  - A wildcard for `*.foocorp.dev`
  - On-demand wildcard for `*.{label}.foocorp.dev` when needed by agents
- ACME state is stored in `~/.config/dev-mode/gateway/acme`.
