# AGENTS

Project instructions for contributors and automation.

- Tests should use temporary directories (via t.TempDir()) instead of absolute paths tied to a specific machine.
- There is no legacy constraint in this project; remove code that is no longer needed.
- Run `go test ./...` after changes; if tests pass, run `go install ./cmd/dev-mode`.

## Quick orientation

- CLI entrypoint: `cmd/dev-mode/main.go`
- CLI command wiring: `internal/cli/`
- Daemon runtime/proxy/process manager: `internal/daemon/`
- Gateway and tunnel code: `internal/gateway/`
- Config loading/types: `internal/config/`
- User-facing docs: `docs/` (especially `docs/config.md`, `docs/usage.md`, `docs/design/`)
