# AGENTS

Project instructions for contributors and automation.

- Tests should use temporary directories (via t.TempDir()) instead of absolute paths tied to a specific machine.
- There is no legacy constraint in this project; remove code that is no longer needed.
- Always run `gofmt` after editing any Go file.
- Run `go test ./...` after changes; if tests pass, run `go install ./cmd/dev`.
- Avoid duplicating reusable helper logic in `internal/cli/` or `internal/daemon/`; move shared resolution/state helpers into `internal/worktree/` or `internal/config/` and reuse them from CLI/daemon commands.

## Quick orientation

- CLI entrypoint: `cmd/dev/main.go`
- CLI command wiring: `internal/cli/`
- Daemon runtime/proxy/process manager: `internal/daemon/`
- Gateway and tunnel code: `internal/gateway/`
- Config loading/types: `internal/config/`
- User-facing docs: `docs/` (especially `docs/config.md`, `docs/usage.md`, `docs/design/`)
