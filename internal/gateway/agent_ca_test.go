package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

func TestCertIssuerIssueClientCert(t *testing.T) {
	t.Parallel()

	issuer, err := NewCertIssuer(t.TempDir())
	if err != nil {
		t.Fatalf("new cert issuer: %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "test-user"},
	}, key)
	if err != nil {
		t.Fatalf("create csr: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	certPEM, caPEM, exp, err := issuer.IssueClientCert(csrPEM, "test-user", 24*time.Hour)
	if err != nil {
		t.Fatalf("issue client cert: %v", err)
	}
	if len(certPEM) == 0 || len(caPEM) == 0 {
		t.Fatalf("expected cert and ca pem")
	}
	if exp.Before(time.Now()) {
		t.Fatalf("expected future expiration")
	}
}

func TestEnsureALPNAddsHTTPProtocols(t *testing.T) {
	t.Parallel()

	got := ensureALPN([]string{"acme-tls/1"})
	hasH2 := false
	hasHTTP11 := false
	for _, proto := range got {
		if proto == "h2" {
			hasH2 = true
		}
		if proto == "http/1.1" {
			hasHTTP11 = true
		}
	}
	if !hasH2 || !hasHTTP11 {
		t.Fatalf("expected h2 and http/1.1 in ALPN list, got %+v", got)
	}
}

func TestNextACMERetry_UsesRetryAfterWhenPresent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 2, 17, 0, 0, 0, time.UTC)
	err := errors.New(`HTTP 429 urn:ietf:params:acme:error:rateLimited - too many failed authorizations, retry after 2026-02-02 18:12:38 UTC`)
	got := nextACMERetry(err, now)
	want := time.Date(2026, 2, 2, 18, 12, 38, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expected retry %s, got %s", want, got)
	}
}

func TestNextACMERetry_DoesNotSuppressContextCancellation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 2, 17, 0, 0, 0, time.UTC)
	err := errors.New("solving challenges: context canceled")
	got := nextACMERetry(err, now)
	if !got.Equal(now) {
		t.Fatalf("expected immediate retry for context cancellation, got %s", got)
	}
}

func TestNormalizeResolvers_Default(t *testing.T) {
	t.Parallel()

	got := normalizeResolvers(nil)
	if len(got) != 1 || got[0] != "1.1.1.1" {
		t.Fatalf("expected default resolver 1.1.1.1, got %+v", got)
	}
}

func TestNormalizeResolvers_DedupesAndTrims(t *testing.T) {
	t.Parallel()

	got := normalizeResolvers([]string{" 1.1.1.1 ", "8.8.8.8:53", "1.1.1.1", ""})
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8:53" {
		t.Fatalf("unexpected resolvers: %+v", got)
	}
}
