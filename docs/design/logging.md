# Logging and Retention

This document specifies logging behavior and retention for dev-mode.

## Log Locations

- Daemon log: `~/.local/state/dev-mode/daemon.log`
- Process logs: `~/.local/state/dev-mode/<project>/worktrees/<slug>/<process>.log`

## Retention Policy

- Each managed process log is capped at **20MB**.
- The daemon log is capped at **20MB**.
- When a log exceeds the cap, the oldest content should be truncated so the newest 20MB are retained.

## Viewing logs

- `dev-mode logs` tail logs from all processes for current worktree
- `dev-mode logs <process_identifier>` tail logs from specific process (identifier can be qualified with project/worktree:process)
- `dev-mode daemon logs` tail daemon logs.

All of the logs commands should accept these arguments:

- `--[no-]follow` (`-fF`) follow the log file. This is implicitly true if stdin is a TTY
- `--all` instead of tailing, start from the beginning of all available logs
- `-n INT` include this many lines of log output from the tail of the log(s) (default 20)

## Notes

- Retention applies per-log file, not across a directory.
- Truncation should be atomic where possible.
