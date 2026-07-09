package worktree

import (
	"os"
	"path/filepath"
	"testing"

	"dev/internal/config"
)

func TestResolvePathFromProjectSlugUsesRegisteredEntry(t *testing.T) {
	base := t.TempDir()
	mainPath := filepath.Join(base, "repo")
	featurePath := filepath.Join(base, "feature")
	if err := os.MkdirAll(mainPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(featurePath, 0o755); err != nil {
		t.Fatal(err)
	}

	daemonCfg := loadDaemonConfigForTest(t, base)
	if err := Register(daemonCfg, Registration{
		Identifier: Identifier{Project: "demo", Slug: "feature"},
		Path:       featurePath,
		MainPath:   mainPath,
	}); err != nil {
		t.Fatalf("register feature: %v", err)
	}

	got, err := ResolvePathFromProjectSlug("demo", "feature", daemonCfg)
	if err != nil {
		t.Fatalf("ResolvePathFromProjectSlug: %v", err)
	}
	if !samePath(got, featurePath) {
		t.Fatalf("expected %s, got %s", featurePath, got)
	}
}

func TestResolvePathFromProjectSlugSupportsConfiguredMainSlug(t *testing.T) {
	base := t.TempDir()
	mainPath := filepath.Join(base, "repo")
	if err := os.MkdirAll(mainPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[project]\nname=\"demo\"\nmain_slug=\"primary\"\n"
	if err := os.WriteFile(filepath.Join(mainPath, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCfg := loadDaemonConfigForTest(t, base)
	if err := Register(daemonCfg, Registration{
		Identifier: Identifier{Project: "demo", Slug: "feature"},
		Path:       filepath.Join(base, "feature"),
		MainPath:   mainPath,
	}); err != nil {
		t.Fatalf("register feature: %v", err)
	}

	got, err := ResolvePathFromProjectSlug("demo", "primary", daemonCfg)
	if err != nil {
		t.Fatalf("ResolvePathFromProjectSlug configured main slug: %v", err)
	}
	if !samePath(got, mainPath) {
		t.Fatalf("expected %s, got %s", mainPath, got)
	}
}

func loadDaemonConfigForTest(t *testing.T, base string) *config.DaemonConfig {
	t.Helper()
	path := filepath.Join(base, "daemon.toml")
	content := "state_dir = \"" + filepath.Join(base, "state") + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadDaemonConfig(path)
	if err != nil {
		t.Fatalf("load daemon config: %v", err)
	}
	return cfg
}
