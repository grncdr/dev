package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNamedPorts_InjectsPortVarsIntoProcessEnv(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfg := `
[project]
name = "demo"

[process.app]
command = "sh -c \"echo $PORT_HTTP,$PORT_GRPC > ports.txt; trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.app.ports]
http = "random"
grpc = "random"
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	if _, err := m.StartProcessesFromDir(slug, repoDir, []string{"app"}, false); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(func() { _, _ = m.StopWorktreeFromDir(slug, repoDir) })

	portsFile := filepath.Join(repoDir, "ports.txt")
	deadline := time.Now().Add(4 * time.Second)
	var contents string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(portsFile)
		if err == nil {
			contents = strings.TrimSpace(string(data))
			if contents != "" && contents != "," {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	parts := strings.Split(contents, ",")
	if len(parts) != 2 {
		t.Fatalf("expected PORT_HTTP,PORT_GRPC, got %q", contents)
	}
	for i, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || port <= 0 || port > 65535 {
			t.Fatalf("port[%d] not a valid port: %q", i, part)
		}
	}
}

func TestNamedPorts_DependentSeesProcessQualifiedPortVars(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfg := `
[project]
name = "demo"

[process.db]
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.db.ports]
sql = 15432
admin = 15433

[process.api]
command = "sh -c \"echo $DEV_PORT_DB_SQL,$DEV_PORT_DB_ADMIN > dep_ports.txt; trap 'exit 0' INT TERM; while :; do sleep 1; done\""
needs = ["db"]
`
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	if _, err := m.StartProcessesFromDir(slug, repoDir, []string{"api"}, false); err != nil {
		t.Fatalf("start api: %v", err)
	}
	t.Cleanup(func() { _, _ = m.StopWorktreeFromDir(slug, repoDir) })

	depFile := filepath.Join(repoDir, "dep_ports.txt")
	deadline := time.Now().Add(4 * time.Second)
	var contents string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(depFile)
		if err == nil {
			contents = strings.TrimSpace(string(data))
			if contents != "" && contents != "," {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if contents != "15432,15433" {
		t.Fatalf("expected DEV_PORT_DB_SQL=15432,DEV_PORT_DB_ADMIN=15433; got %q", contents)
	}
}

func TestNamedPorts_MatcherPortSelectsNamedTarget(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Two pre-allocated listeners so the test can verify which one a matcher
	// resolves to without spinning up a real HTTP server.
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpLn.Close()
	defer grpcLn.Close()
	httpPort := httpLn.Addr().(*net.TCPAddr).Port
	grpcPort := grpcLn.Addr().(*net.TCPAddr).Port

	cfg := fmt.Sprintf(`
[project]
name = "demo"

[process.app]
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.app.ports]
http = %d
grpc = %d

[[process.app.proxy]]
subdomain = "api"
path = "/"
port = "http"

[[process.app.proxy]]
subdomain = "grpc"
path = "/"
port = "grpc"
`, httpPort, grpcPort)
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	if _, err := m.StartProcessesFromDir(slug, repoDir, []string{"app"}, false); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(func() { _, _ = m.StopWorktreeFromDir(slug, repoDir) })

	httpNetwork, httpAddress, err := m.EnsureProcessTargetForRuntimeFromDir(slug, repoDir, "app", "http")
	if err != nil {
		t.Fatalf("ensure http: %v", err)
	}
	if httpNetwork != "tcp" || httpAddress != fmt.Sprintf("127.0.0.1:%d", httpPort) {
		t.Fatalf("expected http target 127.0.0.1:%d, got %s/%s", httpPort, httpNetwork, httpAddress)
	}

	grpcNetwork, grpcAddress, err := m.EnsureProcessTargetForRuntimeFromDir(slug, repoDir, "app", "grpc")
	if err != nil {
		t.Fatalf("ensure grpc: %v", err)
	}
	if grpcNetwork != "tcp" || grpcAddress != fmt.Sprintf("127.0.0.1:%d", grpcPort) {
		t.Fatalf("expected grpc target 127.0.0.1:%d, got %s/%s", grpcPort, grpcNetwork, grpcAddress)
	}
}

func TestNamedPorts_HealthProbesNamedTarget(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Pre-bound listener: the http target is reachable; grpc is unbound and
	// would fail a probe. health.port = "http" must select the right one.
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpLn.Close()
	httpPort := httpLn.Addr().(*net.TCPAddr).Port
	grpcLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcPort := grpcLn.Addr().(*net.TCPAddr).Port
	_ = grpcLn.Close()

	cfg := fmt.Sprintf(`
[project]
name = "demo"

[process.app]
command = "sh -c \"trap 'exit 0' INT TERM; while :; do sleep 1; done\""

[process.app.ports]
http = %d
grpc = %d

[process.app.health]
type = "tcp"
port = "http"

[[process.app.proxy]]
subdomain = "api"
path = "/"
port = "http"
`, httpPort, grpcPort)
	if err := os.WriteFile(filepath.Join(repoDir, ".dev.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("demo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(repoDir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", oldHome) })
	_ = os.Setenv("HOME", baseDir)

	m := NewManager()
	slug := defaultSlugForRepo(t, repoDir)
	if _, err := m.StartProcessesFromDir(slug, repoDir, []string{"app"}, false); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(func() { _, _ = m.StopWorktreeFromDir(slug, repoDir) })

	// If health probed the grpc port, ensure would time out. Success here
	// means the probe correctly selected the named http target.
	if _, _, err := m.EnsureProcessTargetForRuntimeFromDir(slug, repoDir, "app", "http"); err != nil {
		t.Fatalf("expected http target to become ready via health.port=http: %v", err)
	}
}
