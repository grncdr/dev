package cli

import (
	"os"
	"path/filepath"
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
