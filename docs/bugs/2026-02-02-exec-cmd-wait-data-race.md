# Data Race in Process Management

**Date:** 2026-02-02
**Status:** Fixed

## Problem

The daemon's process management had data races detected by Go's race detector when running tests with `-race`:

```
WARNING: DATA RACE
Read at 0x00c00007b328 by goroutine 51:
  os/exec.(*Cmd).awaitGoroutines()
  os/exec.(*Cmd).Wait()
  dev-mode/internal/daemon.(*Manager).stopWorktreeFromDir.func1()
      manager.go:347

Previous write at 0x00c00007b328 by goroutine 46:
  os/exec.(*Cmd).awaitGoroutines.func1()
  os/exec.(*Cmd).Wait()
  dev-mode/internal/daemon.(*Manager).startWorktreeFromDir.func1()
      manager.go:242
```

### Root Cause

Two goroutines were calling `cmd.Wait()` on the same `exec.Cmd`:

1. **Start goroutine** (`manager.go:241-260`): A goroutine launched when a process starts that calls `cmd.Wait()` to handle process exit cleanup.

2. **Stop goroutine** (`manager.go:347`): When stopping a process, another goroutine was created to call `cmd.Wait()` with a timeout.

`exec.Cmd.Wait()` can only be called once per command - calling it multiple times causes a data race because the internal state is being modified and read concurrently.

Additionally, `health.go:142` was reading `cmd.ProcessState` directly while `Wait()` was writing to it.

## Solution

1. **Added exit tracking to `processInfo`:**
   ```go
   type processInfo struct {
       // ... existing fields ...
       exited  chan struct{} // closed when process exits
       exitErr error         // exit error from Wait()
   }
   ```

2. **Single `Wait()` call:** Only the start goroutine calls `Wait()`. When the process exits, it stores the error and closes the `exited` channel.

3. **Channel-based exit detection:** Instead of calling `Wait()` again, `stopWorktreeFromDir` now waits on the `exited` channel:
   ```go
   select {
   case <-time.After(2 * time.Second):
       _ = info.cmd.Process.Kill()
       <-info.exited
   case <-info.exited:
   }
   ```

4. **Safe exit checking:** Added `hasExited()` method that uses a non-blocking channel check:
   ```go
   func (p *processInfo) hasExited() bool {
       select {
       case <-p.exited:
           return true
       default:
           return false
       }
   }
   ```

5. **Removed direct `ProcessState` access:** All checks for `cmd.ProcessState.Exited()` were replaced with `info.hasExited()`.

## Files Changed

- `internal/daemon/manager.go`
- `internal/daemon/health.go`

## Testing

Run the race detector to verify the fix:
```bash
go test -race ./internal/daemon/...
```
