package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGatewayURLForLogin_FromProjectConfig(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".dev.toml")
	if err := os.WriteFile(projectPath, []byte(`
[project]
name = "Foo Corp"

[gateway]
url = "https://gw.example.test"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &Options{}
	opts.ResolvedPaths.ProjectConfig = projectPath
	got := resolveGatewayURLForLogin(opts, "")
	if got != "https://gw.example.test" {
		t.Fatalf("unexpected gateway url: %q", got)
	}
}

func TestResolveGatewayURLForLogin_ExplicitWins(t *testing.T) {
	got := resolveGatewayURLForLogin(nil, "https://flag.example.test")
	if got != "https://flag.example.test" {
		t.Fatalf("unexpected gateway url: %q", got)
	}
}
