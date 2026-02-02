# BUG-001: mTLS Authentication Not Enforced for Gateway Agents

**Severity**: High
**Status**: Open
**Found**: 2026-02-02 (security audit)

## Summary

The gateway design specifies mTLS for agent authentication, but the implementation is incomplete. Agent endpoints (`/_agent/*`) are currently unauthenticated.

## Expected Behavior

Per `docs/design/gateway.md`:

> "HTTP/2 over mTLS between agent and gateway. Gateway validates agent client cert chain against configured CA. Agent validates gateway server cert."

And:

> "mTLS required for agent connectivity. Registration auth bound to cert identity."

Agents should authenticate to the gateway using client certificates obtained via `dev-mode gateway login`.

## Actual Behavior

1. **Gateway server does not require client certificates**
   - `internal/gateway/certs.go`: TLS config from certmagic doesn't set `ClientAuth` or `ClientCAs`
   - `internal/gateway/server.go:86-88`: TLS listener accepts any connection

2. **Agent does not send client certificates**
   - `internal/daemon/tunnels.go:73-108`: Agent created without `GatewayClient`
   - `internal/gateway/agent.go:142-148`: Falls back to default `http.Client` with no certs

3. **Credentials are stored but never loaded**
   - `internal/cli/gateway.go:296-318`: `gateway login` correctly stores `client-key.pem`, `client.pem`, `ca.pem`
   - No code loads these credentials when creating tunnel connections

## Impact

- Any client that can reach the gateway can register labels and establish tunnels
- Invite codes provide initial credential issuance, but issued credentials aren't verified on subsequent connections
- The `/_agent/*` endpoints bypass basic auth (by design, assuming mTLS), leaving them unprotected

## Reproduction

1. Start gateway: `dev-mode gateway run`
2. Without running `gateway login`, directly call:
   ```bash
   curl -X POST https://gateway.example.com/_agent/register \
     -H "Content-Type: application/json" \
     -d '{"label": "attacker", "project": "evil"}'
   ```
3. Registration succeeds without any authentication

## Fix Required

### Gateway Server (`internal/gateway/server.go`, `internal/gateway/certs.go`)

1. Load the agent CA certificate (from `internal/gateway/agent_ca.go`)
2. Configure TLS with client authentication:

```go
tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
tlsConfig.ClientCAs = agentCAPool
```

3. Apply this config to the listener

### Agent/Daemon (`internal/daemon/tunnels.go`)

1. Load client credentials from `~/.config/dev-mode/gateway/credentials/<host>/`
2. Create `GatewayClient` with client certificate:

```go
cert, err := tls.LoadX509KeyPair(certPath, keyPath)
// ...
agent := &gateway.Agent{
    GatewayClient: &http.Client{
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{
                Certificates: []tls.Certificate{cert},
                RootCAs:      caPool,
            },
        },
    },
    // ...
}
```

3. Also configure the raw TLS dialer in `agent.go:connectTunnel()` with client certs

### Additional Considerations

- Handle missing credentials gracefully (prompt user to run `gateway login`)
- Certificate refresh before expiry (design mentions auto-renewal)
- Certificate revocation checking (design mentions revocation by serial/name)

## Related Files

- `docs/design/gateway.md` - Design specification
- `internal/gateway/server.go` - Gateway HTTP server
- `internal/gateway/certs.go` - ACME/TLS configuration
- `internal/gateway/agent.go` - Agent client
- `internal/gateway/agent_ca.go` - CA for issuing client certs
- `internal/daemon/tunnels.go` - Daemon tunnel management
- `internal/cli/gateway.go` - Gateway login command

## Resolution

Implemented fixes to enforce mTLS for gateway agents and use issued credentials end-to-end:

1. **Gateway mTLS enforcement for agent APIs**
   - Gateway now configures TLS client verification using the agent CA (`ClientCAs`).
   - Agent endpoints now require verified client certificates:
     - `/_agent/register`
     - `/_agent/heartbeat`
     - `/_agent/unregister`
     - `/_agent/tunnel/*`
   - `/_agent/cert/issue` remains invite-based (no mTLS requirement), as intended for bootstrap.

2. **Daemon/agent now loads and presents client certs**
   - Tunnel startup loads credentials from:
     - `~/.config/dev-mode/gateway/credentials/<host>/...`, or
     - `<gateway.data_dir>/agent-credentials/<host>/...`
   - These credentials are now used for:
     - agent HTTP registration calls (`GatewayClient`)
     - raw TLS CONNECT tunnel dials (`TLSConfig`)
   - Missing credentials now produce a clear error telling users to run `dev-mode gateway login`.

3. **Supporting certificate plumbing**
   - Added agent CA pool helper to provide trusted client CA material to gateway TLS config.

4. **Tests added/updated**
   - Gateway test verifies agent-auth guard behavior.
   - Daemon test verifies missing-credential failure path and login hint.
   - Full test suite passes with these changes.
