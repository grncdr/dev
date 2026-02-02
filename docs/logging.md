# Logging

This document describes where dev-mode writes logs.

## Daemon logs

- The daemon logs to stdout/stderr by default.
- If run under a service manager, check that manager’s log output.

## Process logs

- Per-process logs are written in the worktree state directory:
  - `~/.local/state/dev-mode/<project>/worktrees/<slug>/<process>.log`

## Proxy logs

- The local proxy uses the daemon’s log output (stdout/stderr).

## Gateway logs

- The gateway runs in the foreground and logs to stdout/stderr.
