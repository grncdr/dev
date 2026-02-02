package daemon

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

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

func TestRewriteResponseBody_Text(t *testing.T) {
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
