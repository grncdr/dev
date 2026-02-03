package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCA(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "ca-key.pem")
	certPath := filepath.Join(dir, "ca.pem")

	key, cert, err := ensureCA(keyPath, certPath)
	if err != nil {
		t.Fatalf("ensureCA: %v", err)
	}
	if key == nil || cert == nil {
		t.Fatalf("expected key and cert")
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected key file: %v", err)
	}
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected cert file: %v", err)
	}
}

func TestRunCertExport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	certDir := filepath.Join(home, ".config", "dev-mode", "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatalf("mkdir cert dir: %v", err)
	}
	want := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(filepath.Join(certDir, "ca.pem"), []byte(want), 0o644); err != nil {
		t.Fatalf("write ca cert: %v", err)
	}

	var out bytes.Buffer
	if err := runCertExport(&out); err != nil {
		t.Fatalf("runCertExport: %v", err)
	}
	if got := out.String(); got != want {
		t.Fatalf("unexpected export output: got %q want %q", got, want)
	}
}

func TestRunCertExportMissingCA(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	err := runCertExport(&out)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "run dev-mode cert install") {
		t.Fatalf("expected install hint, got: %v", err)
	}
}
