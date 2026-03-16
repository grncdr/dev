package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"

	"dev/internal/config"
)

func TestGatewayCredentialRenewer_WaitsUntilRenewWindow(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestGatewayCredentials(dir, "test-agent", time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	renewed := false
	renewer := &gatewayCredentialRenewer{
		gatewayURL:    "https://gw.example.test",
		credentialDir: dir,
		renewBefore:   48 * time.Hour,
		retryEvery:    10 * time.Minute,
		now:           func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			now = now.Add(d)
			return context.Canceled
		},
		renew: func(string, string) (time.Time, error) {
			renewed = true
			return time.Time{}, nil
		},
	}

	err := renewer.run(context.Background())
	if err != context.Canceled {
		t.Fatalf("expected cancellation after scheduled sleep, got %v", err)
	}
	if len(sleeps) != 1 || sleeps[0] != 24*time.Hour {
		t.Fatalf("expected one 24h sleep before renewal window, got %+v", sleeps)
	}
	if renewed {
		t.Fatalf("did not expect renewal attempt before entering the renewal window")
	}
}

func TestGatewayCredentialRenewer_RetriesUntilSuccess(t *testing.T) {
	dir := t.TempDir()
	expiry := time.Date(2026, 3, 13, 13, 0, 0, 0, time.UTC)
	if err := writeTestGatewayCredentials(dir, "test-agent", expiry); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	renewer := &gatewayCredentialRenewer{
		gatewayURL:    "https://gw.example.test",
		credentialDir: dir,
		renewBefore:   48 * time.Hour,
		retryEvery:    10 * time.Minute,
		now:           func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			now = now.Add(d)
			return nil
		},
		renew: func(string, string) (time.Time, error) {
			attempts++
			if attempts == 1 {
				return time.Time{}, os.ErrPermission
			}
			cancel()
			return expiry.Add(30 * 24 * time.Hour), nil
		},
	}

	err := renewer.run(ctx)
	if err != context.Canceled {
		t.Fatalf("expected cancellation after successful renewal, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 renewal attempts, got %d", attempts)
	}
	if len(sleeps) != 1 || sleeps[0] != 10*time.Minute {
		t.Fatalf("expected one retry sleep of 10m, got %+v", sleeps)
	}
}

func TestGatewayCredentialRenewer_StopsAtExpiry(t *testing.T) {
	dir := t.TempDir()
	expiry := time.Date(2026, 3, 13, 12, 5, 0, 0, time.UTC)
	if err := writeTestGatewayCredentials(dir, "test-agent", expiry); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	attempts := 0
	renewer := &gatewayCredentialRenewer{
		gatewayURL:    "https://gw.example.test",
		credentialDir: dir,
		renewBefore:   48 * time.Hour,
		retryEvery:    10 * time.Minute,
		now:           func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			now = now.Add(d)
			return nil
		},
		renew: func(string, string) (time.Time, error) {
			attempts++
			return time.Time{}, os.ErrPermission
		},
	}

	if err := renewer.run(context.Background()); err != nil {
		t.Fatalf("expected clean stop at expiry, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one renewal attempt before expiry, got %d", attempts)
	}
	if len(sleeps) != 1 || sleeps[0] != 5*time.Minute {
		t.Fatalf("expected wait capped to certificate expiry, got %+v", sleeps)
	}
}

func writeTestGatewayCredentials(dir, commonName string, expiresAt time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             expiresAt.Add(-time.Hour),
		NotAfter:              expiresAt,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return config.WriteGatewayCredentials(dir, config.GatewayCredentialMaterial{
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		CAPEM:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	})
}
