package logfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestReadAllConcatenatesRotated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path+RotatedSuffix, "older-")
	writeFile(t, path, "newer")

	data, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "older-newer" {
		t.Fatalf("unexpected concatenation: %q", string(data))
	}
}

func TestReadAllOnlyCurrent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path, "newer")

	data, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "newer" {
		t.Fatalf("unexpected: %q", string(data))
	}
}

func TestReadAllOnlyRotated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path+RotatedSuffix, "older")

	data, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "older" {
		t.Fatalf("unexpected: %q", string(data))
	}
}

func TestReadAllMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.log")
	if _, err := ReadAll(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestReadTailSpansRotated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path+RotatedSuffix, "0123456789")
	writeFile(t, path, "abcde")

	data, err := ReadTail(path, 8)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if string(data) != "789abcde" {
		t.Fatalf("unexpected tail: %q", string(data))
	}
}

func TestReadTailWithinCurrent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path+RotatedSuffix, "0123456789")
	writeFile(t, path, "abcdefghij")

	data, err := ReadTail(path, 4)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if string(data) != "ghij" {
		t.Fatalf("unexpected tail: %q", string(data))
	}
}

func TestReadTailNoRotated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.log")
	writeFile(t, path, "abcde")

	data, err := ReadTail(path, 8)
	if err != nil {
		t.Fatalf("ReadTail: %v", err)
	}
	if string(data) != "abcde" {
		t.Fatalf("unexpected tail: %q", string(data))
	}
}

func TestReadTailMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.log")
	if _, err := ReadTail(path, 8); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}
