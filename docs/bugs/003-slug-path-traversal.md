# BUG-003: Path Traversal Risk in Slug Resolution

**Severity**: Medium
**Status**: Open
**Found**: 2026-02-02 (security audit)

## Summary

Slug resolution accepts user input without strict character validation. While current code uses `filepath.Base` for matching, the lack of validation creates risk if slug values are used in path construction elsewhere.

## Current Behavior

`worktree/resolve.go:18-41`:

```go
func ResolvePathFromSlugInDir(slug, dir string) (string, error) {
    if slug == "" {
        return "", errors.New("slug is required")
    }

    // Allow qualified slugs like project/slug; use the last segment for matching.
    if strings.Contains(slug, "/") {
        parts := strings.Split(slug, "/")
        slug = parts[len(parts)-1]
    }

    entries, err := listWorktreesInDir(dir)
    // ... matches against git worktree output
}
```

The code:
1. Accepts slashes in slugs
2. Extracts the last segment for matching
3. No validation of allowed characters

## Risk

Current implementation is safe because it only matches against git worktree output. However:

1. Future code changes might use slugs directly in path construction
2. Slugs are used in tunnel labels and could leak to other systems
3. No defense-in-depth against malformed input

## Recommended Fix

Add strict validation that slugs contain only safe characters:

```go
var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func ValidateSlug(slug string) error {
    if !validSlugPattern.MatchString(slug) {
        return fmt.Errorf("invalid slug: must contain only alphanumeric characters, hyphens, and underscores")
    }
    return nil
}
```

Apply validation at:
- `worktree/resolve.go` - slug resolution
- `internal/gateway/server.go` - label registration
- `internal/cli/tunnel.go` - tunnel open/close

## Related Files

- `internal/worktree/resolve.go` - Slug resolution logic
- `internal/gateway/server.go` - Label registration

## Resolution

Fixed by adding `ValidateSlug()` function in `internal/worktree/resolve.go`:

```go
var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func ValidateSlug(slug string) error {
    if slug == "" {
        return errors.New("slug is required")
    }
    if !validSlugPattern.MatchString(slug) {
        return errors.New("invalid slug: must start with alphanumeric and contain only alphanumeric, hyphens, or underscores")
    }
    return nil
}
```

Validation is applied in `ResolvePathFromSlugInDir()` after extracting the last segment from qualified slugs.

Tests added in `internal/worktree/slug_test.go`.
