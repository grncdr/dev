package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

const issuedCertValidity = 365 * 24 * time.Hour

type proxyCertProvider struct {
	mu          sync.Mutex
	defaultCert tls.Certificate
	caKey       *ecdsa.PrivateKey
	caCert      *x509.Certificate
	cache       map[string]tls.Certificate
}

func newProxyCertProvider(defaultCert tls.Certificate, caKeyPath, caCertPath string) (*proxyCertProvider, error) {
	caKey, caCert, err := loadCAFromFiles(caKeyPath, caCertPath)
	if err != nil {
		return nil, err
	}
	return &proxyCertProvider{
		defaultCert: defaultCert,
		caKey:       caKey,
		caCert:      caCert,
		cache:       map[string]tls.Certificate{},
	}, nil
}

func (p *proxyCertProvider) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello == nil {
		return &p.defaultCert, nil
	}
	serverName := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hello.ServerName), "."))
	if serverName == "" || strings.Contains(serverName, "*") {
		return &p.defaultCert, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if cert, ok := p.cache[serverName]; ok {
		return &cert, nil
	}

	issued, err := issueLeafCert(serverName, p.caKey, p.caCert)
	if err != nil {
		return nil, err
	}
	p.cache[serverName] = issued
	cert := p.cache[serverName]
	return &cert, nil
}

func loadCAFromFiles(keyPath, certPath string) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("invalid CA key")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("invalid CA cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func issueLeafCert(serverName string, caKey *ecdsa.PrivateKey, caCert *x509.Certificate) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randSerial128()
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: serverName,
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(issuedCertValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{serverName},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der, caCert.Raw},
		PrivateKey:  key,
		Leaf:        template,
	}, nil
}

func randSerial128() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
