# macOS Privileged Port Binding

macOS allows non-root processes to bind to privileged ports (< 1024) on **wildcard addresses** but **not on loopback addresses**.

## Behavior Summary

| Address | Port 80/443 | Result |
|---------|-------------|--------|
| `0.0.0.0` (IPv4 wildcard) | Works | |
| `::` (IPv6 wildcard) | Works | |
| `127.0.0.1` (IPv4 loopback) | Permission denied | |
| `::1` (IPv6 loopback) | Permission denied | |

## Implications

- Servers like Caddy bind to `*` (wildcard) and work without root
- Development proxies that bind to `127.0.0.1:443` will fail without root
- To run a local-only server on port 443, either:
  - Run as root
  - Bind to `0.0.0.0:443` (exposes to network)
  - Use a port > 1024

## Test Program

```python
import socket

def test_bind(addr, port, family):
    try:
        s = socket.socket(family, socket.SOCK_STREAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind((addr, port))
        s.close()
        return "OK"
    except PermissionError:
        return "Permission denied"
    except Exception as e:
        return str(e)

print("macOS privileged port binding test")
print("=" * 40)
print(f"0.0.0.0:443   -> {test_bind('0.0.0.0', 443, socket.AF_INET)}")
print(f"127.0.0.1:443 -> {test_bind('127.0.0.1', 443, socket.AF_INET)}")
print(f"[::]:443      -> {test_bind('::', 443, socket.AF_INET6)}")
print(f"[::1]:443     -> {test_bind('::1', 443, socket.AF_INET6)}")
```

## Linux Comparison

On Linux, binding to any address on a privileged port requires either:
- Running as root
- The `CAP_NET_BIND_SERVICE` capability: `sudo setcap cap_net_bind_service=+ep ./binary`
