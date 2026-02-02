# dev-mode Config

This doc describes the config files and options used by dev-mode.

## Project config (committed)

Path: `.dev-mode.toml` in the repo root.

You can generate a starter config with `dev-mode init`, which creates `.dev-mode.toml` in the current directory and defaults `project.name` to the directory basename.

Required fields:

```toml
[project]
name = "Foo Corp"     # required
main_slug = "foocorp" # optional, applies only to main worktree host slug
```

Optional sections:

```toml
[gateway]
url = "https://gateway.foocorp.dev"

[processes.postgres]
singleton = true
command = "devbox services start postgresql"
stop_command = "devbox services stop postgresql"
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"
port = "random"

[processes.rails]
singleton = false
command = "rails server"
port = "unix"
wrapper = "bundle exec $COMMAND"
health = { type = "http", path = "/up" }
startup_timeout = 45.0

[processes.rails.env]
RAILS_ENV = "development"
PORT = "3000"

[commands]
wrapper = "devbox run $COMMAND"

[[processes.rails.proxy]]
subdomain = null
path = "/"
match = "prefix"
priority = 0

[[processes.rails.proxy]]
subdomain = "app"
path = "/admin"
match = "exact"
priority = 0

[[processes.rails.proxy]]
subdomains = ["api", "assets"]
path = "/"
match = "prefix"

[[processes.node.proxy]]
subdomain = "*"
path = "/api"
match = "prefix"
priority = 0

[[processes.postgres.proxy]]
tcp_listen = 15432

[hooks]
post_create = "bin/setup-worktree"
pre_cleanup = "bin/teardown-worktree"
pre_start = "bin/pre-start"
post_start = "bin/post-start"
pre_stop = "bin/pre-stop"
post_stop = "bin/post-stop"
```

Notes:
- `project.name` is required.
- `project.main_slug` is optional and only changes the main worktree host slug.
- All runnable units live under `processes.*`; use `singleton = true` for project-wide services.
- Hooks run in the worktree directory: `post_create` after `worktree new`, `pre_cleanup` before `worktree cleanup`, and `pre/post start/stop` around `worktree start/stop`.
- The `${MAIN_WORKTREE}` and `${WORKTREE_STATE}` variables are expanded by dev-mode at runtime.
- `port` controls how the process is reached by the proxy:
  - `port = "unix"` uses a unix socket at `${WORKTREE_STATE}/<process>.sock`.
  - `port = "random"` allocates a high localhost port and sets `PORT` / `DEV_MODE_PORT` env vars.
  - `port = 3000` uses that fixed localhost TCP port.
- When `port = "unix"`, the daemon also injects `DEV_MODE_SOCKET` and `DEV_MODE_SOCKET_<PROCESS>` env vars (uppercase, non-alnum → `_`).
- Health checks are optional via `processes.<name>.health`:
  - `health = { type = "http", path = "/health" }` waits for HTTP 2xx/3xx before proxying traffic.
  - `health = { type = "tcp" }` waits for a successful TCP connect (you can also set `port = <int>` inside health to probe a specific port).
- `startup_timeout` is optional per process (decimal seconds) and overrides health timeout when waiting for startup readiness.
- Proxy routing uses per-process matchers. Requests are matched by subdomain first (explicit beats `*` wildcard), then longest path match, then highest `priority`, then process name.
- `subdomain = null` matches `<slug>.<apex_zone>`. `subdomain = "app"` matches `app.<slug>.<apex_zone>`. `subdomain = "*"` matches any subdomain under `<slug>.<apex_zone>`.
- `subdomains = [...]` is also supported in a matcher to map multiple subdomains in one block.
- `path` defaults to `/`, `match` defaults to `prefix`, and `priority` defaults to `0`.
- `tcp_listen = <port>` on a matcher opens a local TCP proxy listener on `127.0.0.1:<port>` that auto-starts the process and forwards raw TCP.
- `match = "prefix"` matches segment boundaries (e.g. `/blah` matches `/blah/..`). Use `/foo*` to match raw prefixes.
- `commands.wrapper` wraps any command execution; use `$COMMAND` to inject the command tokens. Each process can override with `processes.<name>.wrapper`.
- See `docs/logging.md` for log locations.

## Project local override (not committed)

Path: `.dev-mode.local.toml` in the repo root.

This file overrides `.dev-mode.toml` for your local machine.

## User config (trusted worktrees)

Path: `~/.config/dev-mode/config.toml`

```toml
[worktrees."/abs/path/to/foocorp/monorepo"]
project = "Foo Corp"
trusted = true

[gateway]
enabled = false
listen = ":443"
http_listen = ":80"
acme_email = "tech@foocorp.dev"
acme_storage = "~/.config/dev-mode/gateway/acme"
acme_directory = "https://acme-v02.api.letsencrypt.org/directory"
dns_provider = "route53"

[proxy]
apex_zone = ".localhost"
listen_http = "0.0.0.0:80"
listen_https = "0.0.0.0:443"
allow = "loopback"
```

Notes:
- Trust is stored per absolute worktree path under `[worktrees."/abs/path"]`.
- The daemon prompts on first use and records the trust decision.
- `proxy.apex_zone` is user-level and defaults to `.localhost`.

## CLI overrides

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--user-config <path>`: override user config path (default: `~/.config/dev-mode/config.toml`).
- `dev-mode config show`: shows the effective project config (with local override applied) and user config.
- `dev-mode config show --output json`: machine-readable output.
