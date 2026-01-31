# dev-mode Config

This doc describes the config files and options used by dev-mode.

## Project config (committed)

Path: `.dev-mode.toml` in the repo root.

Required fields:

```toml
[project]
name = "foocorp"     # required
apex_zone = ".localhost" # required
```

Optional sections:

```toml
[gateway]
url = "https://gateway.foocorp.dev"

[services.postgres]
shared = true
command = ["devbox", "services", "start", "postgresql"]
stop_command = ["devbox", "services", "stop", "postgresql"]
health = "unix:${MAIN_WORKTREE}/.devbox/virtenv/postgresql/.s.PGSQL.15432"

[processes.rails]
scope = "worktree"        # or "project"
command = ["rails", "server"]
socket = "${WORKTREE_STATE}/rails.sock"

[proxy]
rewrite_html = false
```

Notes:
- `project.name` and `project.apex_zone` are required.
- `services.*` run at project scope; `processes.*` can be scoped to `worktree` or `project`.
- The `${MAIN_WORKTREE}` and `${WORKTREE_STATE}` variables are expanded by dev-mode at runtime.

## Project local override (not committed)

Path: `.dev-mode.local.toml` in the repo root.

This file overrides `.dev-mode.toml` for your local machine.

## User config (trusted worktrees)

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

Notes:
- Trust is stored per absolute worktree path under `[worktrees."/abs/path"]`.
- The daemon prompts on first use and records the trust decision.

## CLI overrides

- `--config <path>`: override project config path (default: `.dev-mode.toml`).
- `--user-config <path>`: override user config path (default: `~/.config/dev-mode/config.toml`).
- `dev-mode config show`: shows the effective project config (with local override applied) and user config.
- `dev-mode config show --output json`: machine-readable output.
