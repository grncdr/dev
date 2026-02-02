package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultHealthType       = "tcp"
	defaultHealthInterval   = 200 * time.Millisecond
	defaultHealthTimeout    = 30 * time.Second
	defaultHealthHTTPPath   = "/"
	defaultProbeDialTimeout = 750 * time.Millisecond
)

type processHealthCheck struct {
	Type     string
	Path     string
	Port     int
	Interval time.Duration
	Timeout  time.Duration
}

func parseProcessHealth(raw any) *processHealthCheck {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	check := &processHealthCheck{
		Type:     defaultHealthType,
		Path:     defaultHealthHTTPPath,
		Interval: defaultHealthInterval,
		Timeout:  defaultHealthTimeout,
	}
	if v, ok := m["type"].(string); ok && strings.TrimSpace(v) != "" {
		check.Type = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := m["path"].(string); ok && strings.TrimSpace(v) != "" {
		check.Path = strings.TrimSpace(v)
	}
	if v, ok := asInt(m["port"]); ok {
		check.Port = v
	}
	if v, ok := asInt(m["interval_ms"]); ok && v > 0 {
		check.Interval = time.Duration(v) * time.Millisecond
	}
	if v, ok := asInt(m["timeout_ms"]); ok && v > 0 {
		check.Timeout = time.Duration(v) * time.Millisecond
	}
	return check
}

func asInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func waitForProcessReady(info *processInfo) error {
	if info == nil {
		return errors.New("process info missing")
	}
	for {
		info.mu.Lock()
		if info.ready {
			info.mu.Unlock()
			return nil
		}
		if info.readyErr != nil {
			// Retry readiness probing while the process is still running.
			if info.cmd != nil && (info.cmd.ProcessState == nil || !info.cmd.ProcessState.Exited()) {
				info.readyErr = nil
			} else {
				err := info.readyErr
				info.mu.Unlock()
				return err
			}
		}
		if info.readyWait != nil {
			ch := info.readyWait
			info.mu.Unlock()
			<-ch
			continue
		}
		ch := make(chan struct{})
		info.readyWait = ch
		network := info.network
		address := info.address
		health := info.health
		startupTimeout := info.startupTimeout
		cmd := info.cmd
		info.mu.Unlock()

		err := probeUntilReady(network, address, health, startupTimeout, cmd)

		info.mu.Lock()
		if err == nil {
			info.ready = true
		} else {
			info.readyErr = err
		}
		close(ch)
		info.readyWait = nil
		info.mu.Unlock()
		return err
	}
}

func probeUntilReady(network, address string, health *processHealthCheck, startupTimeout time.Duration, cmd *exec.Cmd) error {
	if network == "" || address == "" {
		return nil
	}
	timeout := defaultHealthTimeout
	interval := defaultHealthInterval
	if health != nil {
		if health.Timeout > 0 {
			timeout = health.Timeout
		}
		if health.Interval > 0 {
			interval = health.Interval
		}
	}
	if startupTimeout > 0 {
		timeout = startupTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if cmd != nil && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return errors.New("process exited before becoming healthy")
		}
		if err := runHealthProbe(network, address, health); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	if lastErr != nil {
		return fmt.Errorf("health check timeout: %w", lastErr)
	}
	return errors.New("health check timeout")
}

func parseProcessStartupTimeout(raw any) time.Duration {
	switch v := raw.(type) {
	case int:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case int64:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case float64:
		if v > 0 {
			return time.Duration(v * float64(time.Second))
		}
	case float32:
		if v > 0 {
			return time.Duration(float64(v) * float64(time.Second))
		}
	}
	return 0
}

func runHealthProbe(network, address string, health *processHealthCheck) error {
	checkType := defaultHealthType
	if health != nil && health.Type != "" {
		checkType = health.Type
	}
	switch checkType {
	case "http":
		path := defaultHealthHTTPPath
		if health != nil && health.Path != "" {
			path = health.Path
		}
		return httpHealthProbe(network, address, path)
	case "tcp":
		target := address
		if health != nil && health.Port > 0 {
			target = fmt.Sprintf("127.0.0.1:%d", health.Port)
			network = "tcp"
		}
		return tcpHealthProbe(network, target)
	default:
		return fmt.Errorf("unsupported health type %q", checkType)
	}
}

func tcpHealthProbe(network, address string) error {
	conn, err := net.DialTimeout(network, address, defaultProbeDialTimeout)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func httpHealthProbe(network, address, path string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://unix" + path
	if network == "tcp" {
		url = "http://" + address + path
	} else if network == "unix" {
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: defaultProbeDialTimeout}
				return dialer.DialContext(ctx, "unix", address)
			},
		}
	} else {
		return fmt.Errorf("unsupported network %q for http health", network)
	}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
