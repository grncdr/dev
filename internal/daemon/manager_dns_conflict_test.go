package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dev/internal/config"
)

func TestLocalDNSHostConflictsLocked_DetectsHostCollision(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "repo-a")
	repoB := filepath.Join(base, "repo-b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repoA, ".dev.toml"), []byte(`
[project]
name = "org/repo-a"

[process.web]
command = "sleep 1"
proxy = { path = "/" }
port = 3000
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoB, ".dev.toml"), []byte(`
[project]
name = "org/repo-b"

[process.web]
command = "sleep 1"
proxy = { path = "/" }
port = 4000
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgB, _, err := config.LoadProjectConfig(filepath.Join(repoB, ".dev.toml"))
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.mu.Lock()
	keyA := runtimeKeyForPath(repoA)
	m.processes[keyA] = map[string]*processInfo{
		"web": {
			cmd:    &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
			exited: make(chan struct{}),
		},
	}
	m.registerWorktreeLocked(keyA, runtimeWorktree{
		Slug:     "main",
		Project:  "org-repo-a",
		Path:     repoA,
		DNSLabel: "main",
	})

	conflicts := m.localDNSHostConflictsLocked(runtimeKeyForPath(repoB), cfgB, "main", map[string]bool{"web": true}, ".localhost")
	m.mu.Unlock()

	if len(conflicts) == 0 {
		t.Fatalf("expected DNS host conflict")
	}
	if !strings.Contains(conflicts[0].Host, "main.localhost") {
		t.Fatalf("expected main.localhost conflict, got %+v", conflicts)
	}
}

func TestLocalDNSHostConflictsLocked_AllowsRemappedSlugHost(t *testing.T) {
	base := t.TempDir()
	repoA := filepath.Join(base, "repo-a")
	repoB := filepath.Join(base, "repo-b")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repoA, ".dev.toml"), []byte(`
[project]
name = "org/repo-a"

[process.web]
command = "sleep 1"
proxy = { path = "/" }
port = 3000
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoB, ".dev.toml"), []byte(`
[project]
name = "org/repo-b"

[local-dns]
overrides = { main = "repo-b-main" }

[process.web]
command = "sleep 1"
proxy = { path = "/" }
port = 4000
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgB, _, err := config.LoadProjectConfig(filepath.Join(repoB, ".dev.toml"))
	if err != nil {
		t.Fatal(err)
	}

	m := NewManager()
	m.mu.Lock()
	keyA := runtimeKeyForPath(repoA)
	m.processes[keyA] = map[string]*processInfo{
		"web": {
			cmd:    &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}},
			exited: make(chan struct{}),
		},
	}
	m.registerWorktreeLocked(keyA, runtimeWorktree{
		Slug:     "main",
		Project:  "org-repo-a",
		Path:     repoA,
		DNSLabel: "main",
	})

	conflicts := m.localDNSHostConflictsLocked(runtimeKeyForPath(repoB), cfgB, "main", map[string]bool{"web": true}, ".localhost")
	m.mu.Unlock()

	if len(conflicts) != 0 {
		t.Fatalf("expected no DNS host conflicts, got %+v", conflicts)
	}
}
