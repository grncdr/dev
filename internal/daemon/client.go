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
	"strings"
	"time"
)

// Client talks to a running daemon over its unix socket.
type Client struct {
	socketPath string
	httpClient *http.Client
	baseURL    string
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

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.doJSON(ctx, http.MethodGet, "/health", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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
	req := WorktreeRequest{Slug: slug, Project: project}
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
	req := WorktreeRequest{Slug: slug, Project: project, Processes: processes, All: all}
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
