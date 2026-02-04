package daemon

import (
	"net/http"
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
