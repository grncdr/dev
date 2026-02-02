package daemon

import "testing"

func TestRewriteLocation(t *testing.T) {
	value := "https://example.com/path"
	got, ok := rewriteLocation(value, "app.foo.localhost")
	if !ok {
		t.Fatalf("expected rewrite")
	}
	if got != "https://app.foo.localhost/path" {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestRewriteCookieDomain(t *testing.T) {
	cookie := "session=abc; Path=/; Domain=.foo.localhost; HttpOnly"
	got := rewriteCookieDomain(cookie, ".localhost", "api.feature.localhost")
	if got != "session=abc; Path=/; Domain=api.feature.localhost; HttpOnly" {
		t.Fatalf("unexpected cookie: %s", got)
	}
}
