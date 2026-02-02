# Local CA and TLS Certificate Design

## Problem

A single wildcard certificate (`*.localhost`) does not cover names with two labels before `localhost`, for example:

- `monorepo.localhost` (covered)
- `app.monorepo.localhost` (not covered)

This causes browser certificate errors for proxied subdomain-of-slug hosts.

## Design

dev-mode uses a local CA model:

1. `dev-mode cert install` creates and trusts a local root CA.
2. The proxy loads that CA keypair at startup.
3. During TLS handshake, the proxy reads SNI (`ClientHello.ServerName`).
4. The proxy issues an in-memory leaf certificate for that exact hostname, signed by the local CA.
5. Issued certificates are cached in memory for reuse while the daemon is running.

This supports:

- `<slug>.<apex_zone>`
- `<subdomain>.<slug>.<apex_zone>`
- other host depths under the configured apex (as requested by clients)

## Files

Certificate assets are stored in `~/.config/dev-mode/certs/`:

- `ca.pem` (local root CA cert)
- `ca-key.pem` (local root CA private key)
- `localhost.pem` and `localhost-key.pem` (default fallback leaf)

## Security Notes

- The CA is local to the user machine and must be explicitly trusted.
- The proxy still enforces `proxy.allow` (default loopback), so only localhost clients can access it by default.
- Per-host leaf certs are not written to disk; they are generated and cached in-memory.
