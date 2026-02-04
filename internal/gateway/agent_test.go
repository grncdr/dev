package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
