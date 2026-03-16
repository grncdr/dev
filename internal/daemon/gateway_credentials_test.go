package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"dev/internal/config"
)

func TestGatewayCredentialTracker_SyncStartsRenewersForAllStoredCredentials(t *testing.T) {
	var started []string
	var mu sync.Mutex
	done := make(chan struct{}, 2)
	tracker := &gatewayCredentialTracker{
		scanEvery: 0,
		list: func(*config.DaemonConfig) ([]gatewayCredentialTarget, error) {
			return []gatewayCredentialTarget{
				{GatewayURL: "https://gw-one.example.test", CredentialDir: "/tmp/one"},
				{GatewayURL: "https://gw-two.example.test", CredentialDir: "/tmp/two"},
			}, nil
		},
		runRenewer: func(_ context.Context, gatewayURL, credentialDir string) error {
			mu.Lock()
			started = append(started, gatewayURL+"|"+credentialDir)
			mu.Unlock()
			done <- struct{}{}
			return context.Canceled
		},
		active: map[string]context.CancelFunc{},
	}

	if err := tracker.sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for renewer %d to start", i+1)
		}
	}
	mu.Lock()
	got := append([]string(nil), started...)
	mu.Unlock()
	slices.Sort(got)
	want := []string{
		"https://gw-one.example.test|/tmp/one",
		"https://gw-two.example.test|/tmp/two",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected renewers started: got %+v want %+v", got, want)
	}
}

func TestGatewayCredentialTracker_SyncStopsRemovedCredentials(t *testing.T) {
	canceled := false
	tracker := &gatewayCredentialTracker{
		active: map[string]context.CancelFunc{
			"/tmp/old": func() { canceled = true },
		},
		list: func(*config.DaemonConfig) ([]gatewayCredentialTarget, error) {
			return []gatewayCredentialTarget{
				{GatewayURL: "https://gw-new.example.test", CredentialDir: "/tmp/new"},
			}, nil
		},
		runRenewer: func(context.Context, string, string) error { return context.Canceled },
	}

	if err := tracker.sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !canceled {
		t.Fatalf("expected removed credential renewer to be canceled")
	}
	if _, ok := tracker.active["/tmp/old"]; ok {
		t.Fatalf("expected removed credential renewer to be deleted")
	}
	if _, ok := tracker.active["/tmp/new"]; !ok {
		t.Fatalf("expected new credential renewer to be tracked")
	}
}

func TestListGatewayCredentialTargets_UsesStoredMetadata(t *testing.T) {
	base := t.TempDir()
	cfg := &config.DaemonConfig{StateDir: base}
	dir, err := config.ResolveGatewayCredentialDir(cfg, "gw.example.test")
	if err != nil {
		t.Fatalf("ResolveGatewayCredentialDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.GatewayCredentialMetaFilename), []byte(`{"gateway_url":"https://gw.example.test:8443"}`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	targets, err := listGatewayCredentialTargets(cfg)
	if err != nil {
		t.Fatalf("listGatewayCredentialTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %+v", targets)
	}
	if targets[0].GatewayURL != "https://gw.example.test:8443" {
		t.Fatalf("expected stored gateway URL, got %q", targets[0].GatewayURL)
	}
}
