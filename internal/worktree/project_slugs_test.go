package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListProjectSlugsFromRegistryIncludesConfiguredMainSlug(t *testing.T) {
	base := t.TempDir()
	mainPath := filepath.Join(base, "repo")
	featurePath := filepath.Join(base, "feature")
	if err := os.MkdirAll(mainPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(featurePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainPath, ".dev.toml"), []byte("[project]\nname=\"demo\"\nmain_slug=\"primary\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	daemonCfg := loadDaemonConfigForTest(t, base)
	if err := Register(daemonCfg, Registration{
		Project:  "demo",
		Slug:     "feature",
		Path:     featurePath,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	slugs, err := ListProjectSlugsFromRegistry(daemonCfg, "demo")
	if err != nil {
		t.Fatalf("ListProjectSlugsFromRegistry: %v", err)
	}
	if len(slugs) != 2 {
		t.Fatalf("expected 2 slugs, got %d (%v)", len(slugs), slugs)
	}
	if slugs[0] != "feature" || slugs[1] != "primary" {
		t.Fatalf("unexpected slugs: %v", slugs)
	}
}
