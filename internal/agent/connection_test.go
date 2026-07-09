package agent

import (
	"strings"
	"testing"

	"dev/internal/worktree"
)

// Two different worktrees may not share a gateway label — that would be a DNS
// collision the gateway can't satisfy. A re-share of the same worktree under its
// existing label is idempotent, and a same-slug/different-project worktree is a
// distinct tunnel that must be allowed.
func TestConnectionConflictForSpec(t *testing.T) {
	conn := NewConnection("https://gw.example.dev", "")
	conn.SeedTunnel(TunnelStatus{Identifier: worktree.Identifier{Project: "cabinet", Slug: "main"}, Label: "shared", Status: "connected"})

	conn.mu.Lock()
	defer conn.mu.Unlock()

	// Different worktree wants the same label -> hard error.
	if _, err := conn.conflictForSpecLocked(TunnelSpec{Identifier: worktree.Identifier{Project: "drawer", Slug: "main"}, Label: "shared"}); err == nil ||
		!strings.Contains(err.Error(), "already in use by cabinet:main") {
		t.Fatalf("expected label-in-use error, got %v", err)
	}

	// Same worktree, same label -> idempotent.
	existing, err := conn.conflictForSpecLocked(TunnelSpec{Identifier: worktree.Identifier{Project: "cabinet", Slug: "main"}, Label: "shared"})
	if err != nil {
		t.Fatalf("idempotent re-share errored: %v", err)
	}
	if existing == nil || existing.Label != "shared" {
		t.Fatalf("expected existing tunnel returned, got %+v", existing)
	}

	// Same worktree, different label -> already-shared error.
	if _, err := conn.conflictForSpecLocked(TunnelSpec{Identifier: worktree.Identifier{Project: "cabinet", Slug: "main"}, Label: "other"}); err == nil ||
		!strings.Contains(err.Error(), "already has label shared") {
		t.Fatalf("expected already-shared error, got %v", err)
	}

	// Different worktree, different label -> no conflict.
	if existing, err := conn.conflictForSpecLocked(TunnelSpec{Identifier: worktree.Identifier{Project: "drawer", Slug: "main"}, Label: "drawer"}); err != nil || existing != nil {
		t.Fatalf("expected no conflict for distinct worktree, got existing=%+v err=%v", existing, err)
	}
}

// Two different projects each have a "main" worktree. They are distinct tunnels
// and must be addressable independently by their project:slug identity.
func TestConnectionDisambiguatesSameSlugAcrossProjects(t *testing.T) {
	conn := NewConnection("https://gw.example.dev", "")
	conn.SeedTunnel(TunnelStatus{Identifier: worktree.Identifier{Project: "cabinet", Slug: "main"}, Label: "cabinet", Status: "connected"})
	conn.SeedTunnel(TunnelStatus{Identifier: worktree.Identifier{Project: "drawer", Slug: "main"}, Label: "drawer", Status: "connected"})

	drawer := conn.Status(worktree.Identifier{Project: "drawer", Slug: "main"})
	if drawer == nil || drawer.Label != "drawer" {
		t.Fatalf("expected drawer tunnel, got %+v", drawer)
	}
	cabinet := conn.Status(worktree.Identifier{Project: "cabinet", Slug: "main"})
	if cabinet == nil || cabinet.Label != "cabinet" {
		t.Fatalf("expected cabinet tunnel, got %+v", cabinet)
	}

	closed, err := conn.Close("", worktree.Identifier{Project: "cabinet", Slug: "main"})
	if err != nil {
		t.Fatalf("close cabinet: %v", err)
	}
	if closed.Label != "cabinet" {
		t.Fatalf("closed wrong tunnel: %+v", closed)
	}
	if remaining := conn.Status(worktree.Identifier{Project: "drawer", Slug: "main"}); remaining == nil {
		t.Fatalf("drawer tunnel should still be open after closing cabinet")
	}
	if gone := conn.Status(worktree.Identifier{Project: "cabinet", Slug: "main"}); gone != nil {
		t.Fatalf("cabinet tunnel should be closed, got %+v", gone)
	}
}
