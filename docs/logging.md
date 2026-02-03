# Logging

This document describes where dev-mode writes logs.

## State directory

All logs are stored under the state directory:

- Default: `~/.local/state/dev-mode/logs/`
- Override via `DEV_MODE_STATE_DIR` environment variable

## Daemon logs

- Path: `<state_dir>/logs/daemon.log`
- The daemon logs to stdout/stderr by default when run in foreground.
- If run under a service manager, check that manager's log output.

## Process logs

- Per-process logs are written at:
  - `<state_dir>/logs/<project>/<slug>/<process>.log`

## Proxy logs

- The local proxy uses the daemon's log output (stdout/stderr).

## Gateway logs

- The gateway runs in the foreground and logs to stdout/stderr.
