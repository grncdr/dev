package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"dev/internal/config"
	"dev/internal/logfile"
	"dev/internal/worktree"
)

// Client talks to a running daemon over its unix socket.
type Client struct {
	socketPath string
	httpClient *http.Client
	baseURL    string
}

func DefaultClient(flags *config.GlobalFlags) (*Client, error) {
	_, _, err := config.LoadDaemonConfig(flags.DaemonConfigPath)
	if err != nil {
		return nil, err
	}
	socketPath, err := ResolveSocketPath()
	if err != nil {
		return nil, err
	}
	return NewClient(socketPath), nil
}

func NewClient(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{Transport: transport, Timeout: 5 * time.Second},
		baseURL:    "http://unix",
	}
}

// Direct dial the unix socket, used only be the 'connect' CLI command
func (c *Client) Dial() (net.Conn, error) {
	return net.Dial("unix", c.socketPath)
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.doJSON(ctx, http.MethodGet, "/health", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) IsRunning() bool {
	if _, err := os.Stat(c.socketPath); err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := c.Health(ctx)
	return err == nil
}

func (c *Client) EnsureDaemonRunning(opts *config.GlobalFlags) error {
	if c.IsRunning() {
		return nil
	}
	if err := removeStaleSocket(c.socketPath); err != nil {
		return err
	}
	daemonConfig, _, err := config.LoadDaemonConfig(opts.DaemonConfigPath)
	if err != nil {
		return err
	}
	args := []string{"daemon", "run", "--config", opts.ConfigPath, "--daemon-config", opts.DaemonConfigPath}
	if opts.Debug {
		args = append(args, "--debug")
	}
	cmd := exec.Command(os.Args[0], args...)
	logFile, logPath, err := openLog(daemonConfig)
	if err != nil {
		return err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", daemonLogPathEnv, logPath))
	if runtime.GOOS != "windows" {
		sysProc := getSysProcAttr()
		cmd.SysProcAttr = &sysProc
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return c.waitForDaemon(logPath, monitorCmdExit(cmd), 3*time.Second)
}

func (c *Client) WorktreeStart(ctx context.Context, slug string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/start", slug, "", "")
}

func (c *Client) WorktreeStartForTarget(ctx context.Context, slug, project, path string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/start", slug, project, path)
}

func (c *Client) WorktreeStop(ctx context.Context, slug string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/stop", slug, "", "")
}

func (c *Client) WorktreeStopForTarget(ctx context.Context, slug, project, path string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/stop", slug, project, path)
}

func (c *Client) WorktreeStatus(ctx context.Context, slug string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/status", slug, "", "")
}

func (c *Client) WorktreeStatusForTarget(ctx context.Context, slug, project, path string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/status", slug, project, path)
}

func (c *Client) RunningWorktrees(ctx context.Context) (*RunningWorktreesResponse, error) {
	var resp RunningWorktreesResponse
	if err := c.doJSON(ctx, http.MethodGet, "/worktrees/running", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ProcessStart(ctx context.Context, slug string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/start", slug, "", "", processes, all)
}

func (c *Client) ProcessStartForTarget(ctx context.Context, slug, project, path string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/start", slug, project, path, processes, all)
}

func (c *Client) ProcessStop(ctx context.Context, slug string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/stop", slug, "", "", processes, all)
}

func (c *Client) ProcessStopForTarget(ctx context.Context, slug, project, path string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/stop", slug, project, path, processes, all)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/shutdown", map[string]string{"action": "shutdown"}, nil)
}

func (c *Client) WaitForDaemonStopped(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active, err := socketActive(c.socketPath)
		if err == nil && !active {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("daemon did not stop in time")
}

func (c *Client) TunnelOpen(ctx context.Context, req TunnelRequest) (*TunnelStatus, error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, errors.New("TunnelRequest.Path is required")
	}
	var resp TunnelStatus
	if err := c.doJSON(ctx, http.MethodPost, "/tunnels/open", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) TunnelClose(ctx context.Context, req TunnelRequest) (*TunnelStatus, error) {
	var resp TunnelStatus
	if err := c.doJSON(ctx, http.MethodPost, "/tunnels/close", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) TunnelsStatus(ctx context.Context) (*TunnelsResponse, error) {
	var resp TunnelsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/tunnels/status", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) worktreeAction(ctx context.Context, path, slug, project, dirHint string) (*WorktreeStatus, error) {
	var resp WorktreeStatus
	req := WorktreeRequest{Identifier: worktree.Identifier{Project: project, Slug: slug}}
	if dirHint != "" {
		req.Path = dirHint
	} else if cwd, err := os.Getwd(); err == nil {
		req.Path = cwd
	}
	if err := c.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) processAction(ctx context.Context, path, slug, project, dirHint string, processes []string, all bool) (*WorktreeStatus, error) {
	var resp WorktreeStatus
	req := WorktreeRequest{Identifier: worktree.Identifier{Project: project, Slug: slug}, Processes: processes, All: all}
	if dirHint != "" {
		req.Path = dirHint
	} else if cwd, err := os.Getwd(); err == nil {
		req.Path = cwd
	}
	if err := c.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var buf *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(payload)
	} else {
		buf = bytes.NewReader(nil)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, buf)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		code, msg := readErrorMessage(res.Body)
		if msg != "" && code != "" {
			return fmt.Errorf("daemon error: %s (%s: %s)", res.Status, code, msg)
		}
		if msg != "" {
			return fmt.Errorf("daemon error: %s (%s)", res.Status, msg)
		}
		return fmt.Errorf("daemon error: %s", res.Status)
	}

	if out == nil {
		return nil
	}

	return json.NewDecoder(res.Body).Decode(out)
}

func readErrorMessage(r io.Reader) (string, string) {
	body, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil || len(body) == 0 {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		code, _ := payload["code"].(string)
		if val, ok := payload["error"].(string); ok && val != "" {
			return code, val
		}
	}
	msg := strings.TrimSpace(string(body))
	return "", msg
}

func (c *Client) waitForDaemon(logPath string, exitCh <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case err := <-exitCh:
			if err == nil {
				err = errors.New("daemon exited before becoming healthy")
			}
			return daemonStartFailure(err, lastErr, logPath)
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, err := c.Health(ctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return daemonStartFailure(errors.New("timed out waiting for daemon health check"), lastErr, logPath)
}

func monitorCmdExit(cmd *exec.Cmd) <-chan error {
	if cmd == nil {
		return nil
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	return exitCh
}

func daemonStartFailure(cause, lastErr error, logPath string) error {
	msg := "daemon failed to start"
	if cause != nil {
		msg = fmt.Sprintf("%s: %v", msg, cause)
	}
	if lastErr != nil {
		msg = fmt.Sprintf("%s (last health check: %v)", msg, lastErr)
	}
	logTail, err := readDaemonStartupLogTail(logPath)
	if err != nil {
		return fmt.Errorf("%s; failed to read %s: %w", msg, logPath, err)
	}
	if logTail == "" {
		if strings.TrimSpace(logPath) == "" {
			return errors.New(msg)
		}
		return fmt.Errorf("%s; see %s", msg, logPath)
	}
	return fmt.Errorf("%s\n\nRecent daemon log output from %s:\n%s", msg, logPath, logTail)
}

func readDaemonStartupLogTail(logPath string) (string, error) {
	if strings.TrimSpace(logPath) == "" {
		return "", nil
	}
	data, err := logfile.ReadTail(logPath, daemonStartLogTailBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return lastLogLines(data, daemonStartLogTailLines), nil
}

func lastLogLines(data []byte, lines int) string {
	if lines <= 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ""
	}
	parts := bytes.Split(trimmed, []byte("\n"))
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return string(bytes.Join(parts, []byte("\n")))
}

func socketActive(socketPath string) (bool, error) {
	conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false, nil
	}
	_ = conn.Close()
	return true, nil
}
