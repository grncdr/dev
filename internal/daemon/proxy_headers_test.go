package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyForwardedHeadersTransparent(t *testing.T) {
	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Forwarded-Host", "foo.localhost")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	applyForwardedHeaders(req, true)

	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("expected X-Forwarded-Proto=https, got %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "" {
		t.Fatalf("expected X-Forwarded-Host removed, got %q", got)
	}
	if got := req.Header.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("expected X-Forwarded-For removed, got %q", got)
	}
}

func TestApplyForwardedHeadersReverse(t *testing.T) {
	req := &http.Request{Header: make(http.Header)}
	req.Header.Set("X-Forwarded-Host", "foo.localhost")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	applyForwardedHeaders(req, false)

	if got := req.Header.Get("X-Forwarded-Proto"); got != "https" {
		t.Fatalf("expected X-Forwarded-Proto=https, got %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got == "" {
		t.Fatalf("expected X-Forwarded-Host to remain set")
	}
	if got := req.Header.Get("X-Forwarded-For"); got == "" {
		t.Fatalf("expected X-Forwarded-For to remain set")
	}
}

func TestAuthenticateGatewayTunnelRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://label.public.example.test", nil)
	req.SetBasicAuth("alice", "secret")
	rr := httptest.NewRecorder()

	if ok := authenticateGatewayTunnelRequest(rr, req, "alice", "secret"); !ok {
		t.Fatalf("expected auth success")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected default 200 status, got %d", rr.Code)
	}
}

func TestAuthenticateGatewayTunnelRequest_RejectsInvalidCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://label.public.example.test", nil)
	req.SetBasicAuth("alice", "wrong")
	rr := httptest.NewRecorder()

	if ok := authenticateGatewayTunnelRequest(rr, req, "alice", "secret"); ok {
		t.Fatalf("expected auth failure")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 status, got %d", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatalf("expected WWW-Authenticate header")
	}
}
