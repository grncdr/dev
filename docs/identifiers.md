# Identifier Format

dev uses structured identifiers to reference worktrees and processes.

## Project:Slug Format

Worktrees are identified by `project:slug`:

```
myproject:feature/my-branch
```

- **Project**: The repository identifier (slashes allowed, for example `org/repo`)
- **Slug**: The worktree name (slashes allowed, matching git branch names)

The colon `:` separates project from slug.

For commands that take a worktree slug directly (for example `worktree add` and `worktree cleanup`), you may pass just `slug`; dev infers `project` from the current working directory.

## Process Identifiers

Commands like `attach`, `logs`, `start`, `stop`, `restart`, and `status` accept process identifiers in these forms:

| Format | Example | Description |
|--------|---------|-------------|
| `process` | `rails` | Process in current worktree |
| `slug:process` | `feature/x:rails` | Process in specific worktree |
| `project:slug:process` | `myapp:feature/x:rails` | Fully qualified |
| `slug:*` | `feature/x:*` | All processes in worktree |

When no process identifier is provided, commands operate on all processes in the current worktree.

## DNS Labels and Uniqueness

Since slugs can contain slashes (e.g., `feature/my-branch`), the DNS label is derived from the **last path segment**:

- `feature/my-branch` → DNS label `my-branch`
- `hotfix/urgent-fix` → DNS label `urgent-fix`
- `main` → DNS label `main`

**Important**: The last segment must be unique within a project. Creating a worktree with slug `feature/test` when `test` already exists will fail because both would use DNS label `test`.

## Resolution

When resolving slugs, dev accepts either:

1. The full slug: `feature/my-branch`
2. Just the last segment: `my-branch`

Both resolve to the same worktree due to the uniqueness constraint.

## Examples

```bash
# Create a worktree (project inferred from cwd)
dev worktree add feature/auth-rewrite

# Create a worktree (explicit project:slug format)
dev worktree add myapp:feature/auth-rewrite

# Start all processes in current worktree
dev start

# Start specific process
dev start rails

# Start process in another worktree (by last segment)
dev start auth-rewrite:rails

# Fully qualified
dev start myapp:feature/auth-rewrite:rails

# Attach to process
dev attach feature/auth-rewrite:rails

# View logs
dev logs feature/auth-rewrite:sidekiq
```

## Filesystem Layout

Worktrees are created in nested directories mirroring the slug structure:

```
~/.local/share/dev/worktrees/
└── org/
    └── repo/
        └── feature/
            └── auth-rewrite/   # worktree directory
```
