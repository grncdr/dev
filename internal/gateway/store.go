package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type LeaseStatus string

const (
	LeasePending LeaseStatus = "pending"
	LeaseActive  LeaseStatus = "active"
	LeaseRevoked LeaseStatus = "revoked"
	LeaseExpired LeaseStatus = "expired"
)

// Lease represents a gateway label registration. Each label maps to a single
// agent that can receive tunneled traffic.
type Lease struct {
	// Label is the unique tunnel label (used as a DNS subdomain).
	// Corresponds to agent.TunnelSpec.Label and gatewayproto.RegisterRequest.Label.
	Label string `json:"label"`
	// Project is the project name provided during agent registration.
	Project string `json:"project,omitempty"`
	// Slug is the worktree slug provided during agent registration.
	Slug string `json:"slug,omitempty"`
	// AgentID is the unique agent instance identifier.
	AgentID string `json:"agent_id,omitempty"`
	// Name is the human-readable agent name.
	Name string `json:"name,omitempty"`
	// Status is the lease lifecycle state (pending, active, revoked, expired).
	Status LeaseStatus `json:"status"`
	// CreatedAt is when the lease was first created.
	CreatedAt time.Time `json:"created_at"`
	// LastSeenAt is updated on each agent heartbeat.
	LastSeenAt time.Time `json:"last_seen_at"`
	// ExpiresAt is the optional lease expiration time.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type leaseSnapshot struct {
	Leases []Lease `json:"leases"`
}

// LeaseStore persists gateway label leases to disk as JSON and provides
// thread-safe in-memory access.
type LeaseStore struct {
	path   string
	mu     sync.RWMutex
	leases map[string]Lease
}

func NewLeaseStore(dataDir string) (*LeaseStore, error) {
	if dataDir == "" {
		return nil, errors.New("gateway data dir is required")
	}
	stateDir := filepath.Join(dataDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create gateway state dir: %w", err)
	}

	store := &LeaseStore{
		path:   filepath.Join(stateDir, "leases.json"),
		leases: map[string]Lease{},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *LeaseStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read lease state: %w", err)
	}
	var snap leaseSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode lease state: %w", err)
	}
	now := time.Now().UTC()
	for _, l := range snap.Leases {
		if l.Label == "" {
			continue
		}
		if l.ExpiresAt != nil && now.After(*l.ExpiresAt) {
			l.Status = LeaseExpired
			s.leases[l.Label] = l
			continue
		}
		if l.Status != LeaseRevoked && l.Status != LeaseExpired {
			l.Status = LeasePending
		}
		s.leases[l.Label] = l
	}
	return nil
}

func (s *LeaseStore) upsert(lease Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.Label] = lease
	return s.saveLocked()
}

func (s *LeaseStore) touchActive(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[label]
	if !ok {
		return fmt.Errorf("label %q not found", label)
	}
	lease.Status = LeaseActive
	lease.LastSeenAt = time.Now().UTC()
	s.leases[label] = lease
	return s.saveLocked()
}

func (s *LeaseStore) revoke(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[label]
	if !ok {
		return fmt.Errorf("label %q not found", label)
	}
	lease.Status = LeaseRevoked
	lease.LastSeenAt = time.Now().UTC()
	s.leases[label] = lease
	return s.saveLocked()
}

func (s *LeaseStore) list() []Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Lease, 0, len(s.leases))
	for _, lease := range s.leases {
		out = append(out, lease)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Label < out[j].Label
	})
	return out
}

func (s *LeaseStore) hasActive(label string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.leases[label]
	return ok && lease.Status == LeaseActive
}

func (s *LeaseStore) get(label string) (Lease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.leases[label]
	return lease, ok
}

func (s *LeaseStore) saveLocked() error {
	snap := leaseSnapshot{Leases: make([]Lease, 0, len(s.leases))}
	for _, lease := range s.leases {
		snap.Leases = append(snap.Leases, lease)
	}
	sort.Slice(snap.Leases, func(i, j int) bool {
		return snap.Leases[i].Label < snap.Leases[j].Label
	})
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write lease tmp state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace lease state: %w", err)
	}
	return nil
}
