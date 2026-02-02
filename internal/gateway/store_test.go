package gateway

import (
	"testing"
	"time"
)

func TestLeaseStore_PersistAndRestoreAsPending(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLeaseStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Now().UTC()
	if err := store.upsert(Lease{
		Label:      "alpha",
		Status:     LeaseActive,
		CreatedAt:  now,
		LastSeenAt: now,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	reloaded, err := NewLeaseStore(dir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	leases := reloaded.list()
	if len(leases) != 1 {
		t.Fatalf("expected 1 lease, got %d", len(leases))
	}
	if leases[0].Status != LeasePending {
		t.Fatalf("expected pending after restore, got %s", leases[0].Status)
	}
}
