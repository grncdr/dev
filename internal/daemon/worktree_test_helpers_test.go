package daemon

import (
	"testing"

	"dev-mode/internal/config"
	"dev-mode/internal/worktree"
)

func defaultSlugForRepo(t *testing.T, repoDir string) string {
	t.Helper()
	slug, err := worktree.ResolveDefaultSlug(repoDir, nil)
	if err != nil {
		t.Fatalf("resolve default slug: %v", err)
	}
	return slug
}

func registerWorktreeForTest(t *testing.T, daemonCfg *config.DaemonConfig, project, slug, path, mainPath string) {
	t.Helper()
	if err := worktree.Register(daemonCfg, worktree.Registration{
		Project:  project,
		Slug:     slug,
		Path:     path,
		MainPath: mainPath,
	}); err != nil {
		t.Fatalf("register worktree: %v", err)
	}
}
