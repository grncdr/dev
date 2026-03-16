package agent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"dev/internal/config"
)

func MTLSClientForGatewayURL(gatewayURL, credentialDir string) (*http.Client, *tls.Config, error) {
	parsed, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil {
		return nil, nil, err
	}
	if parsed.Scheme != "https" {
		return nil, nil, nil
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, nil, errors.New("gateway URL host is required")
	}
	credDir := strings.TrimSpace(credentialDir)
	if credDir == "" {
		return nil, nil, fmt.Errorf("missing gateway credentials for %s (run dev gateway login --gateway-url %s)", host, gatewayURL)
	}
	keyPath, certPath, caPath := config.GatewayCredentialPaths(credDir)
	if _, err := os.Stat(keyPath); err != nil {
		return nil, nil, fmt.Errorf("missing gateway credentials for %s (run dev gateway login --gateway-url %s)", host, gatewayURL)
	}
	loadClientCert := func() (tls.Certificate, error) {
		return tls.LoadX509KeyPair(certPath, keyPath)
	}
	cert, err := loadClientCert()
	if err != nil {
		return nil, nil, fmt.Errorf("load gateway client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read gateway CA certificate: %w", err)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if ok := rootCAs.AppendCertsFromPEM(caPEM); !ok {
		return nil, nil, errors.New("invalid gateway CA certificate")
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
		ServerName: host,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			loaded, err := loadClientCert()
			if err != nil {
				return nil, err
			}
			return &loaded, nil
		},
	}
	tlsCfg.Certificates = []tls.Certificate{cert}
	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
	return client, tlsCfg, nil
}
