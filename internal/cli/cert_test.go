package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	certDir := filepath.Join(home, ".config", "dev", "certs")
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
	if !strings.Contains(err.Error(), "run dev cert install") {
		t.Fatalf("expected install hint, got: %v", err)
	}
}

func TestInvokingUserOwnership_FromSudoEnv(t *testing.T) {
	prevGetEUID := getEUID
	getEUID = func() int { return 0 }
	defer func() { getEUID = prevGetEUID }()

	t.Setenv("SUDO_UID", "1001")
	t.Setenv("SUDO_GID", "1002")
	t.Setenv("SUDO_USER", "")

	uid, gid, ok, err := invokingUserOwnership()
	if err != nil {
		t.Fatalf("invokingUserOwnership: %v", err)
	}
	if !ok {
		t.Fatalf("expected ownership resolution")
	}
	if uid != 1001 || gid != 1002 {
		t.Fatalf("unexpected uid/gid: got %d/%d", uid, gid)
	}
}

func TestInvokingUserOwnership_InvalidSudoUID(t *testing.T) {
	prevGetEUID := getEUID
	getEUID = func() int { return 0 }
	defer func() { getEUID = prevGetEUID }()

	t.Setenv("SUDO_UID", "bad")
	t.Setenv("SUDO_GID", "1002")
	t.Setenv("SUDO_USER", "")

	_, _, _, err := invokingUserOwnership()
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestChownCertMaterialToInvoker_ChownsFilesAndDir(t *testing.T) {
	prevGetEUID := getEUID
	prevChown := osChown
	getEUID = func() int { return 0 }
	defer func() {
		getEUID = prevGetEUID
		osChown = prevChown
	}()

	t.Setenv("SUDO_UID", "2001")
	t.Setenv("SUDO_GID", "2002")
	t.Setenv("SUDO_USER", "")

	var calls []string
	osChown = func(path string, uid, gid int) error {
		calls = append(calls, path)
		if uid != 2001 || gid != 2002 {
			t.Fatalf("unexpected uid/gid: %d/%d", uid, gid)
		}
		return nil
	}

	if err := chownCertMaterialToInvoker("/tmp/certs", "/tmp/certs/ca.pem", "/tmp/certs/leaf.pem"); err != nil {
		t.Fatalf("chownCertMaterialToInvoker: %v", err)
	}

	want := []string{"/tmp/certs/ca.pem", "/tmp/certs/leaf.pem", "/tmp/certs"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected chown paths: got %#v want %#v", calls, want)
	}
}

func TestChownCertMaterialToInvoker_ChownError(t *testing.T) {
	prevGetEUID := getEUID
	prevChown := osChown
	getEUID = func() int { return 0 }
	defer func() {
		getEUID = prevGetEUID
		osChown = prevChown
	}()

	t.Setenv("SUDO_UID", "2001")
	t.Setenv("SUDO_GID", "2002")
	t.Setenv("SUDO_USER", "")

	osChown = func(path string, uid, gid int) error {
		return errors.New("boom")
	}

	err := chownCertMaterialToInvoker("/tmp/certs", "/tmp/certs/ca.pem")
	if err == nil || !strings.Contains(err.Error(), "set ownership on /tmp/certs/ca.pem") {
		t.Fatalf("unexpected error: %v", err)
	}
}
