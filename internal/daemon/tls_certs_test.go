package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProxyCertProviderIssuesPerHostCertificates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	caKeyPath, caCertPath, caKey, caCert := writeTestCA(t, dir)
	defaultCert, err := issueLeafCert("localhost", caKey, caCert)
	if err != nil {
		t.Fatalf("issue default cert: %v", err)
	}

	provider, err := newProxyCertProvider(defaultCert, caKeyPath, caCertPath)
	if err != nil {
		t.Fatalf("newProxyCertProvider: %v", err)
	}

	cert, err := provider.getCertificate(&tls.ClientHelloInfo{ServerName: "app.monorepo.localhost"})
	if err != nil {
		t.Fatalf("getCertificate: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("expected certificate bytes")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if err := leaf.VerifyHostname("app.monorepo.localhost"); err != nil {
		t.Fatalf("verify hostname: %v", err)
	}
}

func writeTestCA(t *testing.T, dir string) (string, string, *ecdsa.PrivateKey, *x509.Certificate) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "test CA",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatalf("marshal CA key: %v", err)
	}
	caKeyPath := filepath.Join(dir, "ca-key.pem")
	caCertPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), 0o600); err != nil {
		t.Fatalf("write CA key: %v", err)
	}
	if err := os.WriteFile(caCertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatalf("write CA cert: %v", err)
	}
	return caKeyPath, caCertPath, caKey, caCert
}
