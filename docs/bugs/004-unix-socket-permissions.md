# BUG-004: Unix Socket Permissions Not Explicitly Set

**Severity**: Medium
**Status**: Open
**Found**: 2026-02-02 (security audit)

## Summary

The daemon Unix socket is created without explicit permission restrictions. Socket permissions depend on the user's umask, which could allow other users on multi-user systems to connect to the daemon.

## Current Behavior

`daemon/server.go:33-49`:

```go
func NewServer(socketPath string) (*Server, error) {
    // ...
    if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
        return nil, fmt.Errorf("create socket dir: %w", err)
    }

    ln, err := net.Listen("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
    }
    // No explicit chmod on the socket
}
```

The socket permissions are determined by:
1. The directory permissions (0755 - world-readable)
2. The user's umask (typically 022, resulting in 0755 socket)

## Impact

On multi-user systems, other users could:
- Connect to the daemon socket
- Start/stop processes
- Open/close tunnels
- Access proxy functionality

## Reproduction

```bash
# Start daemon as user A
dev-mode daemon

# As user B, check socket permissions
ls -la ~/.local/state/dev-mode/daemon.sock
# If permissions are 0755 or 0777, user B can connect
```

## Recommended Fix

Explicitly set socket permissions to 0600 after creation:

```go
ln, err := net.Listen("unix", socketPath)
if err != nil {
    return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
}

// Restrict socket to owner only
if err := os.Chmod(socketPath, 0600); err != nil {
    ln.Close()
    return nil, fmt.Errorf("chmod socket: %w", err)
}
```

Additionally, set parent directory to 0700:

```go
if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
    return nil, fmt.Errorf("create socket dir: %w", err)
}
```

## Related Files

- `internal/daemon/server.go` - Socket creation

## Resolution

Fixed in `internal/daemon/server.go`:

1. Changed parent directory permissions from `0755` to `0700`
2. Added explicit `chmod` to `0600` on socket after creation

```go
if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
    return nil, fmt.Errorf("create socket dir: %w", err)
}

ln, err := net.Listen("unix", socketPath)
if err != nil {
    return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
}

// Restrict socket permissions to owner only
if err := os.Chmod(socketPath, 0o600); err != nil {
    ln.Close()
    return nil, fmt.Errorf("chmod socket: %w", err)
}
```
