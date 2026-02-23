# dev Hooks

This document describes hook configuration in project config (`.dev.toml`) and daemon config (`daemon.toml`).

## Hook Scopes

- Project hooks (`.dev.toml` `[hooks]`) support worktree lifecycle hooks and process start/stop hooks.
- Daemon hooks (`~/.config/dev/daemon.toml` `[global-hooks]` and `[project-hooks."<project>"]`) support worktree lifecycle hooks only.

## Project Hook Configuration (`.dev.toml`)

```toml
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

Available project hooks:

- `pre_worktree_add`: runs before `dev worktree add` mutates git/worktree state.
- `post_worktree_add`: runs after `dev worktree add` succeeds.
- `pre_worktree_cleanup`: runs before `dev worktree cleanup` mutates git/worktree state.
- `post_worktree_cleanup`: runs after `dev worktree cleanup` succeeds.
- `pre_start`: runs before `dev start`/`dev worktree start` launches processes.
- `post_start`: runs after process start flow completes.
- `pre_stop`: runs before `dev stop`/`dev worktree stop` stops processes.
- `post_stop`: runs after process stop flow completes.

## Daemon Lifecycle Hook Configuration (`daemon.toml`)

```toml
[global-hooks]
pre_worktree_add = "bin/pre-worktree-add-user"
post_worktree_add = "bin/post-worktree-add-user"
pre_worktree_cleanup = "bin/pre-worktree-cleanup-user"
post_worktree_cleanup = "bin/post-worktree-cleanup-user"

[project-hooks."my/project"]
pre_worktree_add = "bin/project-pre-worktree-add-user"
post_worktree_add = "bin/project-post-worktree-add-user"
pre_worktree_cleanup = "bin/project-pre-worktree-cleanup-user"
post_worktree_cleanup = "bin/project-post-worktree-cleanup-user"
```

Available daemon hook events:

- `pre_worktree_add`
- `post_worktree_add`
- `pre_worktree_cleanup`
- `post_worktree_cleanup`

## Working Directory

Each hook command executes with its current working directory set to the target worktree context:

- `pre_worktree_add`: repository main worktree path.
- `post_worktree_add`: newly created worktree path.
- `pre_worktree_cleanup`: worktree being cleaned up.
- `post_worktree_cleanup`: repository main worktree path (cleanup target no longer exists).
- `pre_start`, `post_start`, `pre_stop`, `post_stop`: target worktree path.

## Wrapper behavior

If `[commands].wrapper` is configured, hook commands are executed through that wrapper (same behavior as managed process commands).
Daemon-level hooks in `daemon.toml` do not use the project wrapper.

## Hook Environment Variables

### Worktree lifecycle hooks

The following variables are injected for worktree lifecycle hooks (`pre/post_worktree_add`, `pre/post_worktree_cleanup`):

- `DEV_PROJECT`: normalized project identifier.
- `DEV_WORKTREE_SLUG`: normalized worktree slug.
- `DEV_WORKTREE_DNS_NAME`: DNS-normalized worktree name + apex zone.
- `DEV_WORKTREE_PATH`: target worktree path.
- `DEV_WORKTREE_BRANCH`: target branch name.
- `DEV_MAIN_WORKTREE`: repository main worktree path.
- `DEV_HOOK_NAME`: hook currently executing (for example `pre_worktree_add`).
- `DEV_OPERATION`: `add` or `cleanup`.
- `DEV_IMPLICIT_TARGET`: `true` when cleanup target was inferred from cwd, otherwise `false`.

### Start/stop hooks

For `pre_start`, `post_start`, `pre_stop`, and `post_stop`, dev provides the same core worktree variables used for process runtime:

- `DEV_PROJECT`
- `DEV_WORKTREE_SLUG`
- `DEV_WORKTREE_DNS_NAME`
- `DEV_WORKTREE_PATH`
- `DEV_WORKTREE_BRANCH`
- `DEV_HOOK_NAME`

## Failure behavior

- `pre_*` hook failure aborts the operation.
- `post_*` hook failure returns an error after mutation may already have happened.

## Worktree Hook Order

- `dev worktree add`: project hook, then daemon `global-hooks`, then daemon `project-hooks`.
- `dev worktree cleanup`: daemon `global-hooks`, then daemon `project-hooks`, then project hook.
