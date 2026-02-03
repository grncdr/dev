package docs

import "testing"

func TestLookupConfig(t *testing.T) {
	data, filename, err := Lookup("config")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if filename != "config.md" {
		t.Fatalf("expected config.md, got %q", filename)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty content")
	}
}

func TestLookupSupportsExtensionAndCase(t *testing.T) {
	_, filename, err := Lookup("ConFiG.md")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if filename != "config.md" {
		t.Fatalf("expected config.md, got %q", filename)
	}
}

func TestLookupUnknown(t *testing.T) {
	_, _, err := Lookup("does-not-exist")
	if err == nil {
		t.Fatalf("expected error")
	}
}
