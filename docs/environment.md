# dev-mode Execution Environment

This document explains where commands run, which environment variables are available, and how to add variables for managed processes.

## Process and hook execution model

- Managed processes are started by the **dev-mode daemon**, not by your interactive shell.
- Hooks are run by dev-mode subprocesses (daemon for start/stop hooks, CLI for worktree add/cleanup hooks), not in your interactive shell session.
- Because of this, hooks and managed processes cannot export env vars back into your shell.

## Managed process runtime environment

When you run `dev-mode start` / `dev-mode worktree start`, each managed process receives:

- Inherited daemon environment (`os.Environ()` from the daemon process)
- Core dev-mode variables:
  - `DEV_MODE_PROJECT`
  - `DEV_MODE_WORKTREE_SLUG`
  - `DEV_MODE_WORKTREE_DNS_NAME`
  - `DEV_MODE_WORKTREE_PATH`
  - `DEV_MODE_WORKTREE_BRANCH`
  - For the main worktree, `DEV_MODE_WORKTREE_SLUG` and `DEV_MODE_WORKTREE_DNS_NAME` use `project.main_slug` when configured.
- Port/socket variables when applicable:
  - `PORT` (kept for common server conventions)
  - `DEV_MODE_PORT`
  - `DEV_MODE_PORT_<PROCESS>`
  - `DEV_MODE_SOCKET`
  - `DEV_MODE_SOCKET_<PROCESS>`
- Any values configured under `[process.<name>.env]` in `.dev-mode.toml`

## Hook environments

### Worktree lifecycle hooks

Applies to:

- `pre_worktree_add`
- `post_worktree_add`
- `pre_worktree_cleanup`
- `post_worktree_cleanup`

Provided variables:

- `DEV_MODE_PROJECT`
- `DEV_MODE_WORKTREE_SLUG`
- `DEV_MODE_WORKTREE_DNS_NAME`
- `DEV_MODE_WORKTREE_PATH`
- `DEV_MODE_WORKTREE_BRANCH`
- `DEV_MODE_HOOK_NAME`
- `DEV_MODE_OPERATION` (`add` or `cleanup`)
- `DEV_MODE_IMPLICIT_TARGET` (`true`/`false`)

### Start/stop hooks

Applies to:

- `pre_start`
- `post_start`
- `pre_stop`
- `post_stop`

Provided variables:

- `DEV_MODE_PROJECT`
- `DEV_MODE_WORKTREE_SLUG`
- `DEV_MODE_WORKTREE_DNS_NAME`
- `DEV_MODE_WORKTREE_PATH`
- `DEV_MODE_WORKTREE_BRANCH`
- `DEV_MODE_HOOK_NAME`

## Adding more runtime variables for managed processes

Use process config env blocks:

```toml
[process.web.env]
RAILS_ENV = "development"
API_BASE_URL = "https://${DEV_MODE_WORKTREE_DNS_NAME}"
```

Notes:

- Values in `[process.<name>.env]` are added to that process at runtime.
- dev-mode expands template variables in command/env values before launching the process.
- If you need shell behavior (pipes, redirects, conditionals), run a shell explicitly in `command` or hook definitions (for example `sh -c "..."`).
