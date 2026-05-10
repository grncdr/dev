package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"dev/internal/logfile"
)

func TestTrimOpenFileToMax(t *testing.T) {
	t.Parallel()

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

func TestAppendWithRotationBelowThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "append.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	file, size, err := appendWithRotation(path, file, 0, []byte("hello"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	file, size, err = appendWithRotation(path, file, size, []byte(" world"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if size != 11 {
		t.Fatalf("expected size 11, got %d", size)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("expected concatenated content, got %q", string(data))
	}
	if _, err := os.Stat(path + logfile.RotatedSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected no rotated file below threshold, got err=%v", err)
	}
}

func TestAppendWithRotationRotatesAtThreshold(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "rotate.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	// Fill to just below cap.
	file, size, err := appendWithRotation(path, file, 0, []byte("aaaaaa"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if size != 6 {
		t.Fatalf("expected size 6, got %d", size)
	}

	// Next write would exceed cap → rotate.
	file, size, err = appendWithRotation(path, file, size, []byte("bbb"), 8)
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 {
		t.Fatalf("expected size 3 after rotation, got %d", size)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "bbb" {
		t.Fatalf("expected current %q, got %q", "bbb", string(current))
	}
	rotated, err := os.ReadFile(path + logfile.RotatedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if string(rotated) != "aaaaaa" {
		t.Fatalf("expected rotated %q, got %q", "aaaaaa", string(rotated))
	}

	// Combined logical view via shared reader.
	combined, err := logfile.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(combined) != "aaaaaabbb" {
		t.Fatalf("expected combined %q, got %q", "aaaaaabbb", string(combined))
	}
}

func TestAppendWithRotationOverwritesPriorRotation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "rotate.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	file, size, err := appendWithRotation(path, file, 0, []byte("AAAA"), 4)
	if err != nil {
		t.Fatal(err)
	}
	file, size, err = appendWithRotation(path, file, size, []byte("BBBB"), 4)
	if err != nil {
		t.Fatal(err)
	}
	file, _, err = appendWithRotation(path, file, size, []byte("CCCC"), 4)
	if err != nil {
		t.Fatal(err)
	}

	rotated, err := os.ReadFile(path + logfile.RotatedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if string(rotated) != "BBBB" {
		t.Fatalf("expected rotated %q (most recent), got %q", "BBBB", string(rotated))
	}
}

func TestAppendLineToCappedPathRotates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	if err := appendLineToCappedPath(path, "first\n", 8); err != nil {
		t.Fatal(err)
	}
	if err := appendLineToCappedPath(path, "second\n", 8); err != nil {
		t.Fatal(err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "second\n" {
		t.Fatalf("expected current %q, got %q", "second\n", string(current))
	}
	rotated, err := os.ReadFile(path + logfile.RotatedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if string(rotated) != "first\n" {
		t.Fatalf("expected rotated %q, got %q", "first\n", string(rotated))
	}
}
