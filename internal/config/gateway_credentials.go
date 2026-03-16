package config

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	GatewayClientKeyFilename      = "client-key.pem"
	GatewayClientCertFilename     = "client.pem"
	GatewayCACertFilename         = "ca.pem"
	GatewayCredentialMetaFilename = "metadata.json"
)

type GatewayCredentialInfo struct {
	CommonName string
	ExpiresAt  time.Time
}

type GatewayCredentialMaterial struct {
	KeyPEM     []byte
	CertPEM    []byte
	CAPEM      []byte
	GatewayURL string
}

type gatewayCredentialMetadata struct {
	GatewayURL string `json:"gateway_url"`
}

func GatewayCredentialPaths(dir string) (keyPath, certPath, caPath string) {
	dir = filepath.Clean(dir)
	return filepath.Join(dir, GatewayClientKeyFilename),
		filepath.Join(dir, GatewayClientCertFilename),
		filepath.Join(dir, GatewayCACertFilename)
}

func LoadGatewayCredentialInfo(dir string) (*GatewayCredentialInfo, error) {
	_, certPath, _ := GatewayCredentialPaths(dir)
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read gateway client certificate: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid gateway client certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse gateway client certificate: %w", err)
	}
	return &GatewayCredentialInfo{
		CommonName: cert.Subject.CommonName,
		ExpiresAt:  cert.NotAfter,
	}, nil
}

func WriteGatewayCredentials(dir string, material GatewayCredentialMaterial) error {
	if len(material.KeyPEM) == 0 || len(material.CertPEM) == 0 || len(material.CAPEM) == 0 {
		return errors.New("gateway credential material is incomplete")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keyPath, certPath, caPath := GatewayCredentialPaths(dir)
	if err := writeGatewayCredentialFile(keyPath, material.KeyPEM); err != nil {
		return err
	}
	if err := writeGatewayCredentialFile(certPath, material.CertPEM); err != nil {
		return err
	}
	if err := writeGatewayCredentialFile(caPath, material.CAPEM); err != nil {
		return err
	}
	if err := writeGatewayCredentialMetadata(dir, material.GatewayURL); err != nil {
		return err
	}
	return nil
}

func LoadGatewayCredentialGatewayURL(dir string) (string, error) {
	path := filepath.Join(dir, GatewayCredentialMetaFilename)
	data, err := os.ReadFile(path)
	if err == nil {
		var meta gatewayCredentialMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			return "", fmt.Errorf("decode gateway credential metadata: %w", err)
		}
		if strings.TrimSpace(meta.GatewayURL) != "" {
			return strings.TrimSpace(meta.GatewayURL), nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read gateway credential metadata: %w", err)
	}
	host := strings.TrimSpace(filepath.Base(dir))
	if host == "" || host == "." || host == string(filepath.Separator) {
		return "", errors.New("gateway credential directory name is invalid")
	}
	return "https://" + host, nil
}

func writeGatewayCredentialMetadata(dir, gatewayURL string) error {
	if strings.TrimSpace(gatewayURL) == "" {
		return nil
	}
	data, err := json.Marshal(gatewayCredentialMetadata{GatewayURL: strings.TrimSpace(gatewayURL)})
	if err != nil {
		return err
	}
	return writeGatewayCredentialFile(filepath.Join(dir, GatewayCredentialMetaFilename), data)
}

func writeGatewayCredentialFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
