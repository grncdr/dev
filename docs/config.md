# dev-mode Config

This doc describes the config files and options used by dev-mode.

## Project config (committed)

Path: `.dev-mode.toml` in the repo root.

You can generate a starter config with `dev-mode init`, which creates `.dev-mode.toml` in the current directory and defaults `project.name` to `<owner>/<repo>` when the git `origin` remote matches that format, otherwise the directory basename.

Required fields:

```toml
[project]
name = "foocorp/monorepo" # required
main_slug = "foocorp" # optional, applies only to main worktree host slug
```

Optional sections:

```toml
[gateway]
url = "https://gateway.foocorp.dev"

[process.postgres]
singleton = true
command = "devbox services start postgresql"
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"
port = "random"

[process.rails]
singleton = false
command = "rails server"
port = "unix"
wrapper = "bundle exec $COMMAND"
health = { type = "http", path = "/up" }
startup_timeout = 45.0

[process.rails.env]
RAILS_ENV = "development"
PORT = "3000"

[commands]
wrapper = "devbox run $COMMAND"

[[process.rails.proxy]]
subdomain = null
path = "/"
match = "prefix"
mode = "reverse"
priority = 0

[[process.rails.proxy]]
subdomain = "app"
path = "/admin"
match = "exact"
priority = 0

[[process.rails.proxy]]
subdomains = ["api", "assets"]
path = "/"
match = "prefix"

[[process.node.proxy]]
subdomain = "*"
path = "/api"
match = "prefix"
priority = 0

[[process.postgres.proxy]]
tcp_listen = 15432

[hooks]
pre_worktree_add = "bin/pre-worktree-add"
post_worktree_add = "bin/post-worktree-add"
pre_worktree_cleanup = "bin/pre-worktree-cleanup"
post_worktree_cleanup = "bin/post-worktree-cleanup"
pre_start = "bin/pre-start"
post_start = "bin/post-start"
pre_stop = "bin/pre-stop"
post_stop = "bin/post-stop"
```

Notes:
- `project.name` is required.
- `project.name` may include `/` (for example `org/repo`) to use nested project paths under `worktree_dir`.
- `project.main_slug` is optional and only changes the main worktree host slug.
- All runnable units live under `process.*`; use `singleton = true` for project-wide services.
- Worktree lifecycle hooks are optional:
  - `pre_worktree_add` and `post_worktree_add`
  - `pre_worktree_cleanup` and `post_worktree_cleanup`
- Start/stop hooks are optional:
  - `pre_start` and `post_start`
  - `pre_stop` and `post_stop`
- The `${MAIN_WORKTREE}` and `${WORKTREE_STATE}` variables are expanded by dev-mode at runtime.
- `port` controls how the process is reached by the proxy:
  - `port = "unix"` uses a unix socket at `${WORKTREE_STATE}/<process>.sock`.
  - `port = "random"` allocates a high localhost port and sets `PORT` / `DEV_MODE_PORT` env vars.
  - `port = 3000` uses that fixed localhost TCP port.
- When `port = "unix"`, the daemon also injects `DEV_MODE_SOCKET` and `DEV_MODE_SOCKET_<PROCESS>` env vars (uppercase, non-alnum → `_`).
- Health checks are optional via `process.<name>.health`:
  - `health = { type = "http", path = "/health" }` waits for HTTP 2xx/3xx before proxying traffic.
  - `health = { type = "tcp" }` waits for a successful TCP connect (you can also set `port = <int>` inside health to probe a specific port).
- `startup_timeout` is optional per process (decimal seconds) and overrides health timeout when waiting for startup readiness.
- Proxy routing uses per-process matchers. Requests are matched by subdomain first (explicit beats `*` wildcard), then longest path match, then highest `priority`, then process name.
- `mode` controls proxy behavior per matcher:
  - `mode = "reverse"` (default): sends `X-Forwarded-*` headers and does not rewrite response headers/body.
  - `mode = "transparent"`: sends `X-Forwarded-Proto`, omits `X-Forwarded-Host`/`X-Forwarded-For`, and rewrites response `Location`/`Set-Cookie`/body links for public host mapping.
- `subdomain = null` matches `<slug>.<apex_zone>`. `subdomain = "app"` matches `app.<slug>.<apex_zone>`. `subdomain = "*"` matches any subdomain under `<slug>.<apex_zone>`.
- `subdomains = [...]` is also supported in a matcher to map multiple subdomains in one block.
- `path` defaults to `/`, `match` defaults to `prefix`, and `priority` defaults to `0`.
- `tcp_listen = <port>` on a matcher opens a local TCP proxy listener on `127.0.0.1:<port>` that auto-starts the process and forwards raw TCP.
- `match = "prefix"` matches segment boundaries (e.g. `/blah` matches `/blah/..`). Use `/foo*` to match raw prefixes.
- `commands.wrapper` wraps any command execution; use `$COMMAND` to inject the command tokens. Each process can override with `process.<name>.wrapper`.
- Managed process runtime includes these core variables:
  - `DEV_MODE_PROJECT`
  - `DEV_MODE_WORKTREE_SLUG`
  - `DEV_MODE_WORKTREE_DNS_NAME` (for example `feature.localhost`)
  - `DEV_MODE_WORKTREE_PATH`
  - `DEV_MODE_WORKTREE_BRANCH`
  - `PORT` (when a process has a resolved port)
- See `docs/environment.md` for full execution-context details and hook/process env tables.
- See `docs/logging.md` for log locations.
- See `docs/hooks.md` for hook lifecycle, working directory, and environment variable details.

## Project local override (not committed)

Path: `.dev-mode.local.toml` in the repo root.

This file overrides `.dev-mode.toml` for your local machine.

## Daemon config

Path: `~/.config/dev-mode/daemon.toml`

Override via `DEV_MODE_DAEMON_CONFIG` environment variable or `--daemon-config` flag.

```toml
[gateway]
enabled = false
listen = ":443"
http_listen = ":80"
dns_zone = "tunnels.foocorp.dev"
hostname = "gw.foocorp.dev"
acme_email = "tech@foocorp.dev"
# acme_storage defaults to <state_dir>/gateway (certmagic creates acme/ subdirectory)
acme_directory = "https://acme-v02.api.letsencrypt.org/directory"
acme_resolvers = ["1.1.1.1"]

[gateway.auth]
enabled = false
username = ""
password = ""

[gateway.route53]
enabled = false
hosted_zone_id = ""
ttl = 60

worktree_dir = "~/.local/state/dev-mode/worktrees"

[local-proxy]
enabled = true
apex_zone = ".localhost"
listen_http = "0.0.0.0:80"
listen_https = "0.0.0.0:443"
allow = "loopback"
```

Notes:
- `local-proxy.enabled` defaults to `true`; set to `false` to disable the local proxy.
- `local-proxy.apex_zone` is daemon-level and defaults to `.localhost`.
- `worktree_dir` is daemon-level and controls where managed worktrees are created.
- `gateway.auth` configures optional HTTP Basic Auth for public gateway requests.
- `gateway.auth` is global (not per project/label).
- Keep `gateway.auth.password` in daemon config only.
- `gateway.dns_zone` is the public DNS suffix served by the gateway (for example `tunnels.foocorp.dev`).
- `gateway.hostname` is the gateway hostname used as the Route53 CNAME target (for example `gw.foocorp.dev`).
- When `gateway.route53.enabled = true`, the gateway upserts Route53 CNAME records for each label:
  - `<label>.<gateway.dns_zone>`
  - `*.<label>.<gateway.dns_zone>`
  - with target `gateway.hostname`
- ACME wildcard certificates use Let’s Encrypt with DNS-01 via Route53:
  - base wildcard `*.{gateway.dns_zone}` is provisioned on startup
  - per-label wildcard `*.{label}.{gateway.dns_zone}` is provisioned when labels register
- `gateway.acme_resolvers` configures recursive DNS resolvers used for ACME DNS-01 propagation checks (default: `["1.1.1.1"]`).
- `gateway.acme_email` is required when ACME wildcard provisioning is enabled.
- `gateway.route53.hosted_zone_id` is used by both DNS record sync and ACME DNS-01 challenges; it must match the zone for `gateway.dns_zone`.

## CLI overrides

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--daemon-config <path>`: override daemon config path (default: `~/.config/dev-mode/daemon.toml`).
- `dev-mode config show`: shows the effective project config (with local override applied) and daemon config.
- `dev-mode config show --output json`: machine-readable output.
