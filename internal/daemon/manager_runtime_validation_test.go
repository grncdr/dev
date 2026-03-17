package daemon

import "testing"

func TestRegisterWorktreeLockedRejectsEmptySlug(t *testing.T) {
	m := NewManager()
	key := runtimeKeyForPath(t.TempDir())

	m.mu.Lock()
	m.registerWorktreeLocked(key, runtimeWorktree{
		Slug:     "",
		Project:  "demo",
		Path:     key,
		DNSLabel: "main",
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
		Slug:     "main",
		Project:  "demo",
		Path:     "",
		DNSLabel: "main",
	})
	m.mu.Unlock()

	if _, ok := m.WorktreeByRuntimeKey(key); ok {
		t.Fatalf("expected empty-path worktree registration to be rejected")
	}
}
