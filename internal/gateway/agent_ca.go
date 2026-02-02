package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

type CertIssuer struct {
	caCert    *x509.Certificate
	caKey     *ecdsa.PrivateKey
	caCertPEM []byte
}

func NewCertIssuer(dataDir string) (*CertIssuer, error) {
	if dataDir == "" {
		return nil, errors.New("gateway data dir is required")
	}
	pkiDir := filepath.Join(dataDir, "pki")
	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(pkiDir, "agent-ca.pem")
	keyPath := filepath.Join(pkiDir, "agent-ca-key.pem")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		return createAgentCA(certPath, keyPath)
	}
	return loadAgentCA(certPath, keyPath)
}

func (i *CertIssuer) IssueClientCert(csrPEM []byte, name string, validFor time.Duration) (certPEM []byte, caPEM []byte, expiresAt time.Time, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, nil, time.Time{}, errors.New("invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, nil, time.Time{}, err
	}
	if validFor <= 0 {
		validFor = 30 * 24 * time.Hour
	}
	now := time.Now().UTC()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: name,
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, i.caCert, csr.PublicKey, i.caKey)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return certPEM, i.caCertPEM, tpl.NotAfter, nil
}

func createAgentCA(certPath, keyPath string) (*CertIssuer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	certTpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "dev-mode gateway agent CA",
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, certTpl, certTpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	return loadAgentCA(certPath, keyPath)
}

func loadAgentCA(certPath, keyPath string) (*CertIssuer, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("invalid CA cert PEM")
	}
	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("invalid CA key PEM")
	}
	caKey, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &CertIssuer{caCert: caCert, caKey: caKey, caCertPEM: certPEM}, nil
}

func (i *CertIssuer) ClientCAPool() (*x509.CertPool, error) {
	if i == nil || len(i.caCertPEM) == 0 {
		return nil, errors.New("agent CA is not initialized")
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(i.caCertPEM); !ok {
		return nil, errors.New("failed to append agent CA certificate")
	}
	return pool, nil
}
