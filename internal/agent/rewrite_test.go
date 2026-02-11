package agent

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRewriteLocation(t *testing.T) {
	t.Parallel()

	value := "https://app.foo.localhost/path"
	got, ok := rewriteLocation(value, ".localhost", "foocorp.dev")
	if !ok {
		t.Fatalf("expected rewrite")
	}
	if got != "https://app.foo.foocorp.dev/path" {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestRewriteCookieDomain(t *testing.T) {
	t.Parallel()

	cookie := "session=abc; Path=/; Domain=.foo.localhost; HttpOnly"
	got := rewriteCookieDomain(cookie, ".localhost", "foocorp.dev")
	if got != "session=abc; Path=/; Domain=.foo.foocorp.dev; HttpOnly" {
		t.Fatalf("unexpected cookie: %s", got)
	}
}

func TestRewriteCookieDomainForTunnel_SwapsTunnelAndLocalLabels(t *testing.T) {
	t.Parallel()

	cookie := "session=abc; Path=/; Domain=app.foocorp.localhost; HttpOnly"
	got := rewriteCookieDomainForTunnel(cookie, "app.foocorp.localhost", "app.bobs-main-branch.public.example.com", ".localhost", "public.example.com")
	if got != "session=abc; Path=/; Domain=app.bobs-main-branch.public.example.com; HttpOnly" {
		t.Fatalf("unexpected cookie: %s", got)
	}
}

func TestRewriteLocationForTunnel_PreservesSubdomainsAndSwapsSuffix(t *testing.T) {
	t.Parallel()

	value := "https://api.app.foocorp.localhost/path"
	got, ok := rewriteLocationForTunnel(value, "app.foocorp.localhost", "app.bobs-main-branch.public.example.com", ".localhost", "public.example.com")
	if !ok {
		t.Fatalf("expected rewrite")
	}
	if got != "https://api.app.bobs-main-branch.public.example.com/path" {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestRewriteRequestOriginForTunnel_PreservesSubdomainsAndSwapsSuffix(t *testing.T) {
	t.Parallel()

	header := make(http.Header)
	header.Set("Origin", "https://api.app.bobs-main-branch.public.example.com")

	rewriteRequestOriginForTunnel(header, "app.bobs-main-branch.public.example.com", "app.foocorp.localhost", "public.example.com", ".localhost")

	if got := header.Get("Origin"); got != "https://api.app.foocorp.localhost" {
		t.Fatalf("unexpected Origin rewrite: %s", got)
	}
}

func TestDerivePublicApex(t *testing.T) {
	t.Parallel()

	got, ok := derivePublicApex("app.slug.localhost", "app.share.foocorp.dev", ".localhost")
	if !ok {
		t.Fatalf("expected derived apex")
	}
	if got != "foocorp.dev" {
		t.Fatalf("unexpected apex: %s", got)
	}
}

func TestRewriteRequestCookieDomainValue(t *testing.T) {
	t.Parallel()

	in := `$Version=1; session=abc; $Domain=".foo.foocorp.dev"; $Path="/"`
	got := rewriteRequestCookieDomainValue(in, "foocorp.dev", "localhost")
	want := `$Version=1; session=abc; $Domain=".foo.localhost"; $Path="/"`
	if got != want {
		t.Fatalf("unexpected cookie rewrite:\n got: %s\nwant: %s", got, want)
	}
}

func TestRewriteRequestOrigin(t *testing.T) {
	t.Parallel()

	header := make(http.Header)
	header.Set("Origin", "https://app.slug.foocorp.dev")

	rewriteRequestOrigin(header, "foocorp.dev", "localhost")

	if got := header.Get("Origin"); got != "https://app.slug.localhost" {
		t.Fatalf("unexpected Origin rewrite: %s", got)
	}
}

func TestRewriteRequestOriginNoMatch(t *testing.T) {
	t.Parallel()

	header := make(http.Header)
	header.Set("Origin", "https://app.slug.example.com")

	rewriteRequestOrigin(header, "foocorp.dev", "localhost")

	if got := header.Get("Origin"); got != "https://app.slug.example.com" {
		t.Fatalf("expected Origin unchanged, got: %s", got)
	}
}

func TestRewriteRequestOriginInvalidValue(t *testing.T) {
	t.Parallel()

	header := make(http.Header)
	header.Set("Origin", "not a url")

	rewriteRequestOrigin(header, "foocorp.dev", "localhost")

	if got := header.Get("Origin"); got != "not a url" {
		t.Fatalf("expected invalid Origin unchanged, got: %s", got)
	}
}

func TestRewriteResponseBody_Text(t *testing.T) {
	t.Parallel()

	body := []byte(`<a href="https://app.feature.localhost/path">x</a>`)
	resp := &http.Response{
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	if err := rewriteResponseBody(resp, "app.feature.localhost", "app.xyzz.foocorp.dev"); err != nil {
		t.Fatalf("rewriteResponseBody: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(got), "https://app.xyzz.foocorp.dev/path") {
		t.Fatalf("expected rewritten body, got: %s", string(got))
	}
}

func TestRewriteResponseBody_Gzip(t *testing.T) {
	t.Parallel()

	plain := []byte(`{"url":"https:\/\/app.feature.localhost\/api"}`)
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	resp := &http.Response{
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(compressed.Bytes())),
		ContentLength: int64(compressed.Len()),
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Encoding", "gzip")
	if err := rewriteResponseBody(resp, "app.feature.localhost", "app.xyzz.foocorp.dev"); err != nil {
		t.Fatalf("rewriteResponseBody: %v", err)
	}
	gotCompressed, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gotCompressed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	gotPlain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	_ = zr.Close()
	if !strings.Contains(string(gotPlain), "app.xyzz.foocorp.dev") {
		t.Fatalf("expected rewritten gzip body, got: %s", string(gotPlain))
	}
}

func TestRewriteResponseBody_SkipsBinary(t *testing.T) {
	t.Parallel()

	body := []byte{0x89, 0x50, 0x4E, 0x47}
	resp := &http.Response{
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	resp.Header.Set("Content-Type", "image/png")
	if err := rewriteResponseBody(resp, "app.feature.localhost", "app.xyzz.foocorp.dev"); err != nil {
		t.Fatalf("rewriteResponseBody: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("expected binary body unchanged")
	}
}

func TestRewriteResponseBody_SkipsLarge(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("a"), maxRewriteBodyBytes+1)
	resp := &http.Response{
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	resp.Header.Set("Content-Type", "text/html")
	if err := rewriteResponseBody(resp, "app.feature.localhost", "app.xyzz.foocorp.dev"); err != nil {
		t.Fatalf("rewriteResponseBody: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("expected body size unchanged, got %d want %d", len(got), len(body))
	}
}
