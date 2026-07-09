package daemon

import (
	"testing"

	"dev/internal/worktree"
)

func TestRegisterWorktreeLockedRejectsEmptySlug(t *testing.T) {
	m := NewManager()
	key := runtimeKeyForPath(t.TempDir())

	m.mu.Lock()
	m.registerWorktreeLocked(key, runtimeWorktree{
		Identifier: worktree.Identifier{Project: "demo", Slug: ""},
		Path:       key,
		DNSLabel:   "main",
	})
	m.mu.Unlock()

	if _, ok := m.WorktreeByRuntimeKey(key); ok {
		t.Fatalf("expected empty-slug worktree registration to be rejected")
	}
}

func TestRegisterWorktreeLockedRejectsEmptyPath(t *testing.T) {
	m := NewManager()
	key := runtimeKeyForPath(t.TempDir())

	m.mu.Lock()
	m.registerWorktreeLocked(key, runtimeWorktree{
		Identifier: worktree.Identifier{Project: "demo", Slug: "main"},
		Path:       "",
		DNSLabel:   "main",
	})
	m.mu.Unlock()

	if _, ok := m.WorktreeByRuntimeKey(key); ok {
		t.Fatalf("expected empty-path worktree registration to be rejected")
	}
}
