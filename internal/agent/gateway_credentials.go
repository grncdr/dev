package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dev/internal/config"
)

type renewGatewayCertRequest struct {
	CSR string `json:"csr"`
}

type gatewayCredentialResponse struct {
	CertPEM   string    `json:"cert_pem"`
	CAPEM     string    `json:"ca_pem"`
	ExpiresAt time.Time `json:"expires_at"`
}

func GenerateGatewayCSR(name string) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: strings.TrimSpace(name)},
	}, key)
	if err != nil {
		return nil, nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return key, csrPEM, nil
}

func RenewGatewayCredentials(gatewayURL, credentialDir string) (time.Time, error) {
	base, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil {
		return time.Time{}, err
	}
	if !strings.EqualFold(base.Scheme, "https") {
		return time.Time{}, fmt.Errorf("gateway credential renewal requires an https gateway URL")
	}
	info, err := config.LoadGatewayCredentialInfo(credentialDir)
	if err != nil {
		return time.Time{}, err
	}
	name := strings.TrimSpace(info.CommonName)
	if name == "" {
		name = "dev-user"
	}
	key, csrPEM, err := GenerateGatewayCSR(name)
	if err != nil {
		return time.Time{}, err
	}
	client, _, err := MTLSClientForGatewayURL(gatewayURL, credentialDir)
	if err != nil {
		return time.Time{}, err
	}
	body, err := json.Marshal(renewGatewayCertRequest{CSR: string(csrPEM)})
	if err != nil {
		return time.Time{}, err
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/_agent/cert/renew"})
	req, err := http.NewRequest(http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return time.Time{}, fmt.Errorf("gateway renew failed: %s (%s)", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out gatewayCredentialResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return time.Time{}, err
	}
	if out.CertPEM == "" || out.CAPEM == "" {
		return time.Time{}, fmt.Errorf("gateway renew failed: empty certificate material")
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return time.Time{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := config.WriteGatewayCredentials(credentialDir, config.GatewayCredentialMaterial{
		KeyPEM:     keyPEM,
		CertPEM:    []byte(out.CertPEM),
		CAPEM:      []byte(out.CAPEM),
		GatewayURL: gatewayURL,
	}); err != nil {
		return time.Time{}, err
	}
	return out.ExpiresAt, nil
}
