package cli

import "testing"

func TestResolverContent(t *testing.T) {
	content := resolverContent()
	if content == "" {
		t.Fatalf("expected resolver content")
	}
	if want := "nameserver 127.0.0.1"; content[:len(want)] != want {
		t.Fatalf("unexpected resolver content: %s", content)
	}
}
