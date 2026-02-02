package gateway

import (
	"testing"
	"time"
)

func TestInviteStoreCreateAndConsume(t *testing.T) {
	store, err := NewInviteStore(t.TempDir())
	if err != nil {
		t.Fatalf("new invite store: %v", err)
	}
	inv, err := store.Create(2*time.Minute, 1)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if inv.Code == "" {
		t.Fatalf("expected invite code")
	}
	if err := store.Consume(inv.Code); err != nil {
		t.Fatalf("consume invite: %v", err)
	}
	if err := store.Consume(inv.Code); err == nil {
		t.Fatalf("expected second consume to fail")
	}
}
