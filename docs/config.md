# dev Config

This doc describes the config files and options used by dev.

## Project config (committed)

Path: `.dev.toml` in the repo root.

You can generate a starter config with `dev init`, which creates `.dev.toml` in the current directory and defaults `project.name` to `<owner>/<repo>` when the git `origin` remote matches that format, otherwise the directory basename.

Required fields:

```toml
[project]
name = "foocorp/monorepo" # required
main_slug = "primary" # optional, logical main-worktree slug alias

[local-dns]
overrides = { main = "foocorp" } # optional, remap slug->host label for proxy/status URLs
```

Optional sections:

```toml
[gateway]
url = "https://gateway.foocorp.dev"

[gateway.auth]
username = "alice"
password = "secret"

[gateway.expose.rails]
mode = "reverse_proxy"
debug_log = ""

[gateway.expose.webpack]
mode = "rewrite"
debug_log = "tmp/gateway-http.log"
rewrite_peer_subdomains = ["minio"] # or ["*"] to rewrite all peer local hosts

[process.postgres]
singleton = true
command = "devbox services start postgresql"
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"
port = "random"

[process.rails]
singleton = false
disabled = false
command = "rails server"
port = "unix"
wrapper = "bundle exec $COMMAND"
health = { type = "http", path = "/up" }
startup_timeout = 45.0
idle_timeout = 300.0
idle_follow = ["webpack"]          # stop/start with proxied process idle lifecycle
needs = ["postgres"]

[process.rails.env]
RAILS_ENV = "development"
PORT = "3000"

# Or, for a process that exposes multiple named listeners:
# [process.api]
# command = "api-server --http $PORT_HTTP --grpc $PORT_GRPC"
# [process.api.ports]
# http = "random"
# grpc = 50051
# [process.api.health]
# type = "tcp"
# port = "http"

[commands]
wrapper = "devbox run $COMMAND"

[[process.rails.proxy]]
subdomain = null
path = "/"
match = "prefix"
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
- `project.main_slug` is optional and sets the logical slug alias for the main worktree.
- `local-dns.overrides` is optional and remaps worktree slugs to proxy DNS labels for host routing/status output.
  - Example: `main = "foocorp"` maps `foocorp.localhost` to the main worktree.
- All runnable units live under `process.*`; use `singleton = true` for project-wide services.
- `disabled = true` prevents that process from starting. If another process lists it in `needs`, that dependency is ignored and the dependent still starts.
- Worktree lifecycle hooks are optional:
  - `pre_worktree_add` and `post_worktree_add`
  - `pre_worktree_cleanup` and `post_worktree_cleanup`
- Start/stop hooks are optional:
  - `pre_start` and `post_start`
  - `pre_stop` and `post_stop`
- The `${MAIN_WORKTREE}` and `${WORKTREE_STATE}` variables are expanded by dev at runtime.
- `port` controls how the process is reached by the proxy:
  - `port = "unix"` uses a unix socket at `${WORKTREE_STATE}/<process>.sock`.
  - `port = "random"` allocates a high localhost port and sets `PORT` / `DEV_PORT` env vars.
  - `port = 3000` uses that fixed localhost TCP port.
- When `port = "unix"`, the daemon also injects `DEV_SOCKET` and `DEV_SOCKET_<PROCESS>` env vars (uppercase, non-alnum → `_`).
- For a process that needs more than one listener, use `[process.<name>.ports]` instead of `port`. The two keys are mutually exclusive; mixing them is a hard error at config load.
  - Each entry maps a name (lowercase letters/digits/underscores, must start with a letter) to a port spec: an integer, `"random"`, or `"unix"`.
  - Inside the process: `PORT_<NAME>=<port>` for tcp/random, `SOCKET_<NAME>=<path>` for unix.
  - Visible to dependents (`needs = [<this>]`): `DEV_PORT_<PROCESS>_<NAME>` and `DEV_SOCKET_<PROCESS>_<NAME>`. No bare `PORT` / `DEV_PORT` / `DEV_SOCKET` / `DEV_PORT_<PROCESS>` are emitted.
  - Unix sockets land at `${WORKTREE_STATE}/<process>-<name>.sock`.
  - Each proxy matcher must select a port via `port = "<name>"` when the process declares more than one entry; with a single entry the selector is optional.
  - The health block must set `port = "<name>"` when more than one entry is declared; otherwise it falls back to the sole entry.
- Health checks are optional via `process.<name>.health`:
  - `health = { type = "http", path = "/health" }` waits for HTTP 2xx/3xx before proxying traffic.
  - `health = { type = "tcp" }` waits for a successful TCP connect.
  - `port = <int>` inside health probes a specific literal TCP port; `port = "<name>"` selects a named entry from `[process.<name>.ports]`.
- `needs` declares dependencies that must be started before this process. It accepts a string or array of process names.
- `idle_follow` lists proxied processes whose idle timeout lifecycle this process should follow: when any listed process is idle-stopped, this process is stopped; when that process auto-starts from new traffic, any processes previously stopped by `idle_follow` auto-start in the background.
- `startup_timeout` is optional per process (decimal seconds) and overrides health timeout when waiting for startup readiness.
- `idle_timeout` is optional per proxied process (decimal seconds); default is `300` seconds (5 minutes). Set `idle_timeout = 0` to disable idle shutdown.
- Proxy routing uses per-process matchers. Requests are matched by subdomain first (explicit beats `*` wildcard), then longest path match, then highest `priority`, then process name.
- `gateway.expose` controls which processes accept gateway-tunneled requests.
  - Only processes listed in `gateway.expose` are reachable from the gateway.
  - `mode = "reverse_proxy"`: sends `X-Forwarded-*` headers and does not rewrite response headers/body.
  - `mode = "rewrite"`: sends `X-Forwarded-Proto`, omits `X-Forwarded-Host`/`X-Forwarded-For`, rewrites `Location`, rewrites `Set-Cookie Domain`, rewrites RFC cookie `$Domain` on incoming requests, and rewrites text response bodies for local/public host mapping.
  - `rewrite_peer_subdomains = ["minio"]`: when `mode = "rewrite"`, also rewrites peer local hosts that match listed subdomains (for example `minio.main.localhost` -> `minio.<share-label>.<gateway-dns-zone>`).
  - `rewrite_peer_subdomains = ["*"]`: wildcard that rewrites all peer local hosts under the local apex.
  - `debug_log = "<path>"`: appends full gateway HTTP request/response transcripts to a log file. Relative paths are resolved from the matched process worktree directory. Response bodies are logged after rewrite handling.
- `subdomain = null` matches `<slug>.<apex_zone>`. `subdomain = "app"` matches `app.<slug>.<apex_zone>`. `subdomain = "*"` matches any subdomain under `<slug>.<apex_zone>`.
- `subdomains = [...]` is also supported in a matcher to map multiple subdomains in one block.
- `path` defaults to `/`, `match` defaults to `prefix`, and `priority` defaults to `0`.
- `tcp_listen = <port>` on a matcher opens a local TCP proxy listener on `127.0.0.1:<port>` that auto-starts the process and forwards raw TCP.
- `match = "prefix"` matches segment boundaries (e.g. `/blah` matches `/blah/..`). Use `/foo*` to match raw prefixes.
- `commands.wrapper` wraps any command execution; use `$COMMAND` to inject the command tokens. Each process can override with `process.<name>.wrapper`.
- Managed process runtime includes these core variables:
  - `DEV_PROJECT`
  - `DEV_WORKTREE_SLUG`
  - `DEV_WORKTREE_DNS_NAME` (for example `feature.localhost`)
  - `DEV_WORKTREE_PATH`
  - `DEV_WORKTREE_BRANCH`
  - `PORT` (when a process has a resolved port)
- See `docs/environment.md` for full execution-context details and hook/process env tables.
- See `docs/logging.md` for log locations.
- See `docs/hooks.md` for hook lifecycle, working directory, and environment variable details.

## Project local override (not committed)

Path: `.dev.local.toml` in the repo root.

This file overrides `.dev.toml` for your local machine.

## Daemon config

Path: `~/.config/dev/daemon.toml`

Override via `DEV_DAEMON_CONFIG` environment variable or `--daemon-config` flag.

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

[gateway.route53]
enabled = false
hosted_zone_id = ""
ttl = 60

worktree_dir = "~/.local/state/dev/worktrees"

[local-proxy]
enabled = true
apex_zone = ".localhost"
listen_http = "0.0.0.0:80"
listen_https = "0.0.0.0:443"
allow = "loopback"

[global-hooks]
pre_worktree_add = "bin/pre-worktree-add-user"
post_worktree_add = "bin/post-worktree-add-user"
pre_worktree_cleanup = "bin/pre-worktree-cleanup-user"
post_worktree_cleanup = "bin/post-worktree-cleanup-user"

[project-hooks."foocorp/monorepo"]
pre_worktree_add = "bin/project-pre-worktree-add-user"
post_worktree_add = "bin/project-post-worktree-add-user"
pre_worktree_cleanup = "bin/project-pre-worktree-cleanup-user"
post_worktree_cleanup = "bin/project-post-worktree-cleanup-user"
```

Notes:
- `local-proxy.enabled` defaults to `true`; set to `false` to disable the local proxy.
- `local-proxy.apex_zone` is daemon-level and defaults to `.localhost`.
- `worktree_dir` is daemon-level and controls where managed worktrees are created.
- `global-hooks` configures user-level worktree lifecycle hooks that apply to all projects.
- `project-hooks."<project-name>"` configures additional user-level hooks for a specific project name (for example `project-hooks."foocorp/monorepo"`).
- Worktree hook execution order:
  - `dev worktree add`: project hook first, then daemon `global-hooks`, then daemon `project-hooks`.
  - `dev worktree cleanup`: daemon `global-hooks` first, then daemon `project-hooks`, then project hook.
- `gateway.auth` in project config provides default `dev share` Basic Auth credentials.
  - `dev share --auth <username:password>` overrides project defaults.
  - `dev share --no-auth` disables project defaults for that share invocation.
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

- `--config <path>`: override project config path (default: `.dev.toml`).
- `--daemon-config <path>`: override daemon config path (default: `~/.config/dev/daemon.toml`).
- `dev config show`: shows the effective project config (with local override applied) and daemon config.
- `dev config show --output json`: machine-readable output.
