package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"dev-mode/internal/config"
)

func TestServer_RegisterPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	srv, client := startGatewayServer(t, dir, config.UserGatewayAuth{})

	body := bytes.NewBufferString(`{"project":"Foo Corp","slug":"main","label":"alpha","agent_id":"a1"}`)
	resp, err := client.Post("http://"+srv.Addr()+"/_agent/register", "application/json", body)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	srv2, client2 := startGatewayServer(t, dir, config.UserGatewayAuth{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	}()

	resp, err = client2.Get("http://" + srv2.Addr() + "/_registry/labels")
	if err != nil {
		t.Fatalf("registry labels: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registry status: %d", resp.StatusCode)
	}
	var payload struct {
		Labels []Lease `json:"labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode labels: %v", err)
	}
	if len(payload.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(payload.Labels))
	}
	if payload.Labels[0].Status != LeasePending {
		t.Fatalf("expected pending label after restart, got %s", payload.Labels[0].Status)
	}
}

func TestServer_BasicAuthAppliesOnlyToPublicRequests(t *testing.T) {
	dir := t.TempDir()
	auth := config.UserGatewayAuth{Enabled: true, Username: "u", Password: "p"}
	srv, client := startGatewayServer(t, dir, auth)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	resp, err := client.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatalf("public request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/_registry/labels", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("registry request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(b))
	}
}

func startGatewayServer(t *testing.T, dataDir string, auth config.UserGatewayAuth) (*Server, *http.Client) {
	t.Helper()
	srv, err := NewServer("127.0.0.1:0", dataDir, auth)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go func() {
		_ = srv.Serve()
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := client.Get("http://" + srv.Addr() + "/_registry/labels")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway did not become ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return srv, client
}
