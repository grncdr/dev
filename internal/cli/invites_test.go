package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunInviteCreate_UsesProjectGatewayURL(t *testing.T) {
	var got struct {
		TTLSeconds int64 `json:"ttl_seconds"`
		Uses       int   `json:"uses"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_agent/invites/create" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"invite_code": "abc123",
			"expires_at":  time.Now().UTC(),
			"uses":        1,
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".dev.toml")
	projectConfig := "[project]\nname = \"Foo\"\n[gateway]\nurl = \"" + srv.URL + "\"\n"
	if err := os.WriteFile(projectPath, []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &Options{ResolvedPaths: ResolvedPaths{
		ProjectConfig: projectPath,
		DaemonConfig:  filepath.Join(dir, "daemon.toml"),
	}}
	if err := runInviteCreate(opts, "", 5*time.Minute, 2); err != nil {
		t.Fatalf("runInviteCreate: %v", err)
	}
	if got.TTLSeconds != 300 {
		t.Fatalf("expected ttl_seconds=300, got %d", got.TTLSeconds)
	}
	if got.Uses != 2 {
		t.Fatalf("expected uses=2, got %d", got.Uses)
	}
}

func TestRunInviteCreate_RequiresGatewayURL(t *testing.T) {
	opts := &Options{ResolvedPaths: ResolvedPaths{
		DaemonConfig: filepath.Join(t.TempDir(), "daemon.toml"),
	}}
	err := runInviteCreate(opts, "", 5*time.Minute, 1)
	if err == nil || !strings.Contains(err.Error(), "gateway URL is required") {
		t.Fatalf("expected missing gateway URL error, got %v", err)
	}
}

func TestRunGatewayInit_CreatesBootstrapInvite(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	daemonPath := filepath.Join(dir, "daemon.toml")
	daemonConfig := "state_dir = \"" + stateDir + "\"\n"
	if err := os.WriteFile(daemonPath, []byte(daemonConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &Options{ResolvedPaths: ResolvedPaths{
		DaemonConfig: daemonPath,
	}}
	if err := runGatewayInit(opts, 5*time.Minute, 1); err != nil {
		t.Fatalf("runGatewayInit: %v", err)
	}
	invitesPath := filepath.Join(stateDir, "gateway", "state", "invites.json")
	if _, err := os.Stat(invitesPath); err != nil {
		t.Fatalf("expected invites file to exist: %v", err)
	}
}
