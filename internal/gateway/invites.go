package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Invite is a time-limited, use-limited token that authorizes a new agent to
// enroll via the cert/issue endpoint.
type Invite struct {
	// Code is the random hex invite token.
	Code string `json:"code"`
	// CreatedAt is when the invite was generated.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is when the invite becomes invalid.
	ExpiresAt time.Time `json:"expires_at"`
	// UsesLeft is the remaining number of times this invite can be consumed.
	UsesLeft int `json:"uses_left"`
	// ConsumedAt is set when the last use is consumed.
	ConsumedAt time.Time `json:"consumed_at,omitempty"`
}

type inviteSnapshot struct {
	Invites []Invite `json:"invites"`
}

// InviteStore persists mTLS enrollment invites to disk as JSON and provides
// thread-safe creation and consumption.
type InviteStore struct {
	path    string
	mu      sync.Mutex
	invites map[string]Invite
}

func NewInviteStore(dataDir string) (*InviteStore, error) {
	if dataDir == "" {
		return nil, errors.New("gateway data dir is required")
	}
	stateDir := filepath.Join(dataDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create gateway state dir: %w", err)
	}
	s := &InviteStore{
		path:    filepath.Join(stateDir, "invites.json"),
		invites: map[string]Invite{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *InviteStore) Create(ttl time.Duration, uses int) (Invite, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if uses <= 0 {
		uses = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	code, err := randomInviteCode()
	if err != nil {
		return Invite{}, err
	}
	now := time.Now().UTC()
	invite := Invite{
		Code:      code,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		UsesLeft:  uses,
	}
	s.invites[invite.Code] = invite
	if err := s.saveLocked(); err != nil {
		return Invite{}, err
	}
	return invite, nil
}

func (s *InviteStore) Consume(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reload from disk in case invites were created externally
	if err := s.load(); err != nil {
		return err
	}
	invite, ok := s.invites[code]
	if !ok {
		return errors.New("invite not found")
	}
	now := time.Now().UTC()
	if now.After(invite.ExpiresAt) {
		return errors.New("invite expired")
	}
	if invite.UsesLeft <= 0 {
		return errors.New("invite already consumed")
	}
	invite.UsesLeft--
	if invite.UsesLeft == 0 {
		invite.ConsumedAt = now
	}
	s.invites[code] = invite
	return s.saveLocked()
}

func (s *InviteStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read invite state: %w", err)
	}
	var snap inviteSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("decode invite state: %w", err)
	}
	for _, inv := range snap.Invites {
		if inv.Code == "" {
			continue
		}
		s.invites[inv.Code] = inv
	}
	return nil
}

func (s *InviteStore) saveLocked() error {
	snap := inviteSnapshot{Invites: make([]Invite, 0, len(s.invites))}
	for _, inv := range s.invites {
		snap.Invites = append(snap.Invites, inv)
	}
	sort.Slice(snap.Invites, func(i, j int) bool { return snap.Invites[i].Code < snap.Invites[j].Code })
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randomInviteCode() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
