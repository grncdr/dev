# BUG-002: Registry Labels Endpoint Exposes Tunnels Without Authentication

**Severity**: Medium
**Status**: Open
**Found**: 2026-02-02 (security audit)

## Summary

The `/_registry/labels` endpoint returns all registered tunnel leases without requiring authentication, allowing anyone who can reach the gateway to enumerate active tunnels.

## Expected Behavior

The endpoint should require authentication (basic auth or mTLS) before returning tunnel information.

## Actual Behavior

`server.go:168-170` explicitly exempts this endpoint from basic auth:

```go
func (s *Server) requiresBasicAuth(path string) bool {
    if strings.HasPrefix(path, "/_agent/") {
        return false
    }
    if path == "/_registry/labels" {
        return false  // <-- No auth required
    }
    return true
}
```

The endpoint returns full lease details:

```go
func (s *Server) handleRegistryLabels(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]any{
        "labels": s.store.list(),
    })
}
```

## Impact

Attackers can enumerate:
- All active tunnel labels
- Project names and slugs
- Developer names (from the `Name` field)
- Activity patterns (created_at, last_seen_at)

This information could be used for:
- Social engineering attacks
- Targeted phishing using project/developer names
- Reconnaissance for further attacks

## Reproduction

```bash
curl https://gateway.example.com/_registry/labels
```

Returns all active leases without credentials.

## Recommended Fix

Option A: Require basic auth for this endpoint (remove the exemption)

Option B: Require mTLS (add `ensureAgentAuth` guard)

Option C: Return only labels without full lease details for unauthenticated requests

## Related Files

- `internal/gateway/server.go` - Endpoint handler and auth exemption
