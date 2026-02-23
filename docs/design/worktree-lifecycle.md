# Worktree Lifecycle

This document defines the lifecycle commands for managed worktrees.

Note: `worktree add` and `worktree cleanup` currently execute in the CLI process. We may want to move execution into the daemon so lifecycle operations run in a cleaner, more controlled environment.

## Commands

```bash
# add a new worktree
dev worktree add project/slug [branch]

# remove an existing worktree
dev worktree cleanup [project/slug] [--delete-branch] [--dry-run] [--force]

# list managed worktrees
dev worktree list
```

## Identifier Model (`project/slug`)

- Required format is exactly `project/slug` (single `/`, both segments required).
- Segments are canonicalized to lowercase before lookup/storage.
- Allowed characters per segment: `a-z`, `0-9`, `-`, `_`.
- Segment length must be `1..63`; full identifier length must be `<=127`.
- Invalid identifiers fail with a descriptive error that includes expected format.

Note: this is currently a local policy for worktree lifecycle commands. We should add a dedicated design doc for shared resolution algorithms across project, slug, and process identifiers.

## Worktree Directory

- Worktrees are created under daemon-configured `worktree_dir`.
- `worktree_dir` is read only from daemon config (projects cannot override it).
- If unset, default is `<dev state dir>/worktrees`.
- Path handling:
  - Expand `~`.
  - Require absolute path.
  - Create directories on demand.
  - Fail if configured path exists as a file.
- Final worktree path is always `<worktree_dir>/<project>/<slug>`.

Example: in project `foobar`, running `dev worktree add foobar/xyz` creates:

`~/.local/state/dev/worktrees/foobar/xyz`

## `worktree add`

`dev worktree add project/slug [branch]`

- Command is strict about target path existence:
  - if `<worktree_dir>/<project>/<slug>` already exists, fail.
- Branch behavior:
  - If `[branch]` is omitted, target branch name is `<slug>`.
  - If `<slug>` branch does not exist, create it at current `HEAD`.
  - If `<slug>` branch already exists, reuse it and print a warning.
  - If `[branch]` is provided, use that branch name.
- Exit status:
  - `0` on success, non-zero on failure.

## `worktree cleanup`

`dev worktree cleanup [project/slug] [--delete-branch] [--dry-run] [--force]`

- Target resolution:
  - If `project/slug` is provided, operate on that target.
  - If omitted, resolve the current managed worktree from current working directory.
  - Resolution must work from any subdirectory under a managed worktree.
  - If omitted and current directory is not inside a managed worktree, fail with a descriptive error.
- Safety rules:
  - Cleanup of the repository main worktree is forbidden and must fail.
  - If target worktree has tracked/untracked changes, fail unless `--force`.
  - If target is in use by current shell location, fail unless `--force`.
  - Metadata/path mismatch should fail clearly unless stale metadata can be unambiguously pruned.
- Branch deletion (`--delete-branch`):
  - Without `--force`, fail if branch is not fully merged.
  - With `--force`, allow deletion.
- `--dry-run`:
  - Runs the same resolution and safety checks as real cleanup.
  - Prints what would be removed/deleted, with no mutations.
- Exit status:
  - `0` on success; non-zero on all failures.

## `worktree list`

`dev worktree list` shows managed worktrees with key state, including:

- `project/slug`
- worktree path
- branch
- status flags (e.g., dirty, current, main)

## Lifecycle Hooks

Creating and cleaning up worktrees trigger project lifecycle hooks:

- `pre-worktree-add`
- `post-worktree-add`
- `pre-worktree-cleanup`
- `post-worktree-cleanup`

Behavior:

- `pre-*` hooks run before mutation; non-zero exit aborts operation.
- `post-*` hooks run after successful mutation.
- `post-*` hook failure makes the command exit non-zero, even though mutation already happened.
- Hook stdout/stderr is surfaced with phase prefix and exit status.

Suggested hook environment:

- `DEV_PROJECT`
- `DEV_WORKTREE_SLUG`
- `DEV_WORKTREE_PATH`
- `DEV_WORKTREE_BRANCH`
- `DEV_MAIN_WORKTREE`
- `DEV_WORKTREE_DNS_NAME`
- `DEV_HOOK_NAME`
- `DEV_OPERATION` (`add` or `cleanup`)
- `DEV_IMPLICIT_TARGET` (`true`/`false`)

## Testing Expectations

Implementations should include:

- unit tests for identifier validation/canonicalization and resolution logic;
- cleanup safety checks (main worktree, dirty state, in-use detection);
- add/cleanup flows, including omitted-branch behavior and warning on reused `<slug>` branch;
- `--dry-run` behavior (no mutation);
- hook ordering and failure semantics.

All filesystem tests should use `t.TempDir()`.
