package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if existing == nil || existing.spec.Label != "shared" {
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

// fakeTunnelGateway accepts registrations and holds each tunnel connection
// open until the test drops it.
type fakeTunnelGateway struct {
	*httptest.Server
	tunnels chan net.Conn
}

func newFakeTunnelGateway(t *testing.T) *fakeTunnelGateway {
	t.Helper()
	gw := &fakeTunnelGateway{tunnels: make(chan net.Conn, 8)}
	gw.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_agent/register":
			_, _ = w.Write([]byte(`{"type":"result","status":"ok","public_host":"example.dev"}`))
		case r.Method == http.MethodConnect && strings.HasPrefix(r.URL.Path, "/_agent/tunnel/"):
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\n\r\n")
			_ = buf.Flush()
			gw.tunnels <- conn
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gw.Close)
	return gw
}

func (gw *fakeTunnelGateway) nextTunnel(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn := <-gw.tunnels:
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tunnel connection")
		return nil
	}
}

func waitForTunnelStatus(t *testing.T, conn *Connection, label, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, ok := conn.TunnelForLabel(label)
		if ok && status.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected tunnel %s to be %q, got %q (found=%v)", label, want, status.Status, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A re-share closes the old tunnel and opens a new one under the same label.
// The old tunnel's runner exits only when the gateway drops its session, which
// can be after the new tunnel connected. Its exit must not change the status
// of the new tunnel.
func TestConnectionReopenKeepsNewTunnelStatusWhenOldRunnerExits(t *testing.T) {
	gw := newFakeTunnelGateway(t)
	conn := NewConnection(gw.URL, "")
	spec := TunnelSpec{Identifier: worktree.Identifier{Project: "cabinet", Slug: "main"}, Label: "shared", GatewayURL: gw.URL}
	handler := func(context.Context, TunnelStatus, *http.Request, net.Conn) error { return nil }

	if _, err := conn.Open(spec, handler); err != nil {
		t.Fatalf("open: %v", err)
	}
	oldTunnel := gw.nextTunnel(t)
	waitForTunnelStatus(t, conn, "shared", "connected")

	if _, err := conn.Close("shared", spec.Identifier); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := conn.Open(spec, handler); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	gw.nextTunnel(t)
	waitForTunnelStatus(t, conn, "shared", "connected")

	// The gateway drops the old session after the new one connected.
	_ = oldTunnel.Close()
	time.Sleep(200 * time.Millisecond)

	status, _ := conn.TunnelForLabel("shared")
	if status.Status != "connected" {
		t.Fatalf("expected new tunnel to stay connected, got %q (last error %q)", status.Status, status.LastError)
	}
}
