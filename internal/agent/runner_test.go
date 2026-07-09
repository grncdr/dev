package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"dev/internal/worktree"
)

func TestDoUpstreamRequest_DoesNotFollowRedirects(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/final")
			w.Header().Set("Set-Cookie", "session=abc; Path=/")
			w.WriteHeader(http.StatusFound)
			return
		}
		if r.URL.Path == "/final" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)

	req, err := http.NewRequest(http.MethodGet, upstream.URL+"/redirect", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := doUpstreamRequest(&http.Client{Timeout: 2 * time.Second}, req)
	if err != nil {
		t.Fatalf("do upstream request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected status %d, got %d", http.StatusFound, resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/final" {
		t.Fatalf("expected location /final, got %q", got)
	}
	if got := resp.Header.Get("Set-Cookie"); got == "" {
		t.Fatalf("expected Set-Cookie to be preserved")
	}
}

func TestAgentRegister_StreamsProgressAndExtendsClientTimeout(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_agent/register" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("stream"); got != "1" {
			http.Error(w, "missing stream=1", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
			http.Error(w, "missing ndjson accept", http.StatusBadRequest)
			return
		}
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		_ = enc.Encode(map[string]string{
			"type":    "progress",
			"stage":   "acme_sync_started",
			"message": "starting ACME DNS-01 issuance",
		})
		_ = enc.Encode(map[string]string{
			"type":        "result",
			"status":      "ok",
			"public_host": "tunnels.example.test",
		})
	}))
	t.Cleanup(gateway.Close)

	var progress []string
	agent := &Agent{
		GatewayURL: gateway.URL,
		Label:      "alpha",
		Identifier: worktree.Identifier{Project: "demo", Slug: "main"},
		GatewayClient: &http.Client{
			Timeout: 1 * time.Millisecond,
		},
		OnRegisterProgress: func(stage, _ string) {
			progress = append(progress, stage)
		},
	}

	if err := agent.register(context.Background()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !slices.Contains(progress, "acme_sync_started") {
		t.Fatalf("expected progress callback, got %+v", progress)
	}
}
