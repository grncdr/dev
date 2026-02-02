# AGENTS

Project instructions for contributors and automation.

- Tests should use temporary directories (via t.TempDir()) instead of absolute paths tied to a specific machine.
- There is no legacy for this project; remove code that is no longer needed right now.
- Run `go install` after every successful test run.
