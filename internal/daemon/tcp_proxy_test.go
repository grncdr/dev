package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTCPProxyAutoStartsSingletonProcess(t *testing.T) {
	baseDir := t.TempDir()
	repoDir := filepath.Join(baseDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runGit(repoDir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate tcp listen: %v", err)
	}
	tcpListenPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	configBody := fmt.Sprintf(`
[project]
name = "demo"

[proxy]
apex_zone = ".localhost"

[process.db]
singleton = true
command = "sh -c \"python3 -m http.server ${PORT}\""
port = "random"
health = { type = "http", path = "/" }

[[process.db.proxy]]
tcp_listen = %d
`, tcpListenPort)
	if err := os.WriteFile(filepath.Join(repoDir, ".dev-mode.toml"), []byte(configBody), 0o600); err != nil {
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
	oldHTTP := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTP")
	oldHTTPS := os.Getenv("DEV_MODE_PROXY_LISTEN_HTTPS")
	oldCwd, _ := os.Getwd()
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", oldHTTP)
		_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", oldHTTPS)
		_ = os.Chdir(oldCwd)
	})
	_ = os.Setenv("HOME", baseDir)
	_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTP", "off")
	_ = os.Setenv("DEV_MODE_PROXY_LISTEN_HTTPS", "off")
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}

	socketPath := filepath.Join(baseDir, "devd.sock")
	if len(socketPath) > 80 {
		socketPath = filepath.Join(os.TempDir(), fmt.Sprintf("devd-tcp-proxy-%d.sock", time.Now().UnixNano()))
	}
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	client := NewClient(socketPath)
	if err := waitForHealth(client, 2*time.Second); err != nil {
		t.Fatalf("daemon not healthy: %v", err)
	}

	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tcpListenPort), 250*time.Millisecond)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial tcp proxy: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, _ = conn.Write([]byte("GET / HTTP/1.0\r\nHost: db\r\n\r\n"))
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	_ = conn.Close()
	if err != nil {
		t.Fatalf("read proxied response: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("expected 200 status line, got %q", strings.TrimSpace(status))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slug := filepath.Base(repoDir)
	st, err := client.WorktreeStatus(ctx, slug)
	if err != nil {
		t.Fatalf("worktree status: %v", err)
	}
	found := false
	for _, p := range st.Processes {
		if p.Name == "db" && p.Status == "running" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected db process running, got %+v", st.Processes)
	}

	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("server did not shut down")
	}
}
