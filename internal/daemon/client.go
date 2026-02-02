package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

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
	return c.worktreeAction(ctx, "/worktrees/start", slug)
}

func (c *Client) WorktreeStop(ctx context.Context, slug string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/stop", slug)
}

func (c *Client) WorktreeStatus(ctx context.Context, slug string) (*WorktreeStatus, error) {
	return c.worktreeAction(ctx, "/worktrees/status", slug)
}

func (c *Client) ProcessStart(ctx context.Context, slug string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/start", slug, processes, all)
}

func (c *Client) ProcessStop(ctx context.Context, slug string, processes []string, all bool) (*WorktreeStatus, error) {
	return c.processAction(ctx, "/processes/stop", slug, processes, all)
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/shutdown", map[string]string{"action": "shutdown"}, nil)
}

func (c *Client) worktreeAction(ctx context.Context, path, slug string) (*WorktreeStatus, error) {
	var resp WorktreeStatus
	req := WorktreeRequest{Slug: slug}
	if cwd, err := os.Getwd(); err == nil {
		req.Path = cwd
	}
	if err := c.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) processAction(ctx context.Context, path, slug string, processes []string, all bool) (*WorktreeStatus, error) {
	var resp WorktreeStatus
	req := WorktreeRequest{Slug: slug, Processes: processes, All: all}
	if cwd, err := os.Getwd(); err == nil {
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
