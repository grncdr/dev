# Windows CI Failures

## Summary

`windows-latest` CI runs are currently failing for multiple reasons that are not reproduced on Linux/macOS.

## Observed failures

From CI run `21642854322`:

1. **CLI build failure on Windows**
   - `internal/cli/daemon.go:126`
   - `internal/cli/daemon.go:315`
   - Error:
     - `cannot use &sysProc (value of type *syscallSysProcAttr) as *syscall.SysProcAttr value in assignment`

2. **Config parsing test failure**
   - `internal/config`: `TestLoadDaemonConfig_GatewayFields`
   - Error:
     - `decode daemon config: toml: non-hex character`

3. **Daemon integration test instability/failures**
   - Multiple tests fail with:
     - `daemon not healthy: health check timeout`
     - `unsupported`
   - Affected tests include random port, resume, shared process, TCP proxy, tunnel lifecycle, and e2e health tests.

## Scope

The failures are specific to `windows-latest` in CI. Linux and macOS jobs pass in the same runs.

## Temporary mitigation

Windows builds are disabled in CI until the Windows-specific compile/runtime/test issues are fixed.

## Follow-up work

- Fix `syscallSysProcAttr` type mismatch in CLI daemon startup code on Windows.
- Investigate TOML parsing discrepancy in Windows test environment.
- Audit daemon/process management tests for Windows support (shell commands, signals, sockets, timing assumptions).
- Re-enable Windows in CI matrix after green runs.
