package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrimOpenFileToMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("0123456789"); err != nil {
		t.Fatal(err)
	}
	size, err := trimOpenFileToMax(file, 4)
	if err != nil {
		t.Fatal(err)
	}
	if size != 4 {
		t.Fatalf("expected size 4, got %d", size)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "6789" {
		t.Fatalf("expected tail retention, got %q", string(data))
	}
}

func TestAppendOpenFileWithRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	size := int64(0)
	size, err = appendOpenFileWithRetention(file, size, []byte("hello"), 8)
	if err != nil {
		t.Fatal(err)
	}
	size, err = appendOpenFileWithRetention(file, size, []byte(" world"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if size != 8 {
		t.Fatalf("expected size 8, got %d", size)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "lo world" {
		t.Fatalf("expected retained tail, got %q", string(data))
	}
}
