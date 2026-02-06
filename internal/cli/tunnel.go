package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/daemon"
	"dev/internal/worktree"
)

func newShareCmd(opts *Options) *cobra.Command {
	var label string
	var gatewayURL string
	cmd := &cobra.Command{
		Use:   "share [slug]",
		Short: "share a worktree through the gateway",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugArg string
			if len(args) > 0 {
				slugArg = args[0]
			}
			return runTunnelOpen(opts, slugArg, label, gatewayURL)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "override tunnel label (defaults to slug)")
	cmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "gateway URL override")
	return cmd
}

func newUnshareCmd(opts *Options) *cobra.Command {
	var label string
	var gatewayURL string
	cmd := &cobra.Command{
		Use:   "unshare [slug]",
		Short: "stop sharing a worktree through the gateway",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugArg string
			if len(args) > 0 {
				slugArg = args[0]
			}
			return runTunnelClose(opts, slugArg, label, gatewayURL)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "close by label")
	cmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "gateway URL override")
	return cmd
}

func runTunnelOpen(opts *Options, slugArg, labelArg, gatewayURLArg string) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}

	slug, path, cfg, err := resolveTunnelConfig(opts, slugArg)
	if err != nil {
		return err
	}
	gatewayURL := strings.TrimSpace(gatewayURLArg)
	if gatewayURL == "" {
		configURL, _ := cfg.Gateway["url"].(string)
		gatewayURL = strings.TrimSpace(configURL)
	}
	if gatewayURL == "" {
		return errors.New("gateway.url is required in project config or pass --gateway-url")
	}
	label := strings.TrimSpace(labelArg)
	if label == "" {
		label = effectiveDisplaySlug(slug, path, cfg)
	}

	req := daemon.TunnelRequest{
		Slug:       slug,
		Label:      label,
		GatewayURL: gatewayURL,
		Project:    cfg.Project.Name,
		Name:       os.Getenv("USER"),
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := client.TunnelOpen(ctx, req)
	if err != nil {
		return err
	}

	fmt.Printf("tunnel %s (%s): %s\n", status.Label, status.Slug, status.Status)
	if publicURL := gatewayPublicURL(gatewayURL, status.PublicHost, label); publicURL != "" {
		fmt.Printf("public base URL: %s\n", publicURL)
	}
	return nil
}

func runTunnelClose(opts *Options, slugArg, labelArg, gatewayURLArg string) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}
	req := daemon.TunnelRequest{Label: strings.TrimSpace(labelArg)}
	if req.Label == "" {
		slug, err := resolveSlug(opts, slugArg)
		if err != nil {
			return err
		}
		req.Slug = slug
	}
	if strings.TrimSpace(gatewayURLArg) == "" {
		_, _, cfg, err := resolveTunnelConfig(opts, slugArg)
		if err != nil {
			return err
		}
		configURL, _ := cfg.Gateway["url"].(string)
		if strings.TrimSpace(configURL) == "" {
			return errors.New("gateway.url is required in project config or pass --gateway-url")
		}
	}
	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := client.TunnelClose(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("tunnel %s (%s): %s\n", status.Label, status.Slug, status.Status)
	return nil
}

func resolveTunnelConfig(opts *Options, slugArg string) (string, string, *config.ProjectConfig, error) {
	slug, err := resolveSlug(opts, slugArg)
	if err != nil {
		return "", "", nil, err
	}
	cwd := workingDir(opts)
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return "", "", nil, err
	}
	path, err := worktree.ResolvePathFromSlugWithRegistry(slug, cwd, daemonCfg)
	if err != nil {
		return "", "", nil, err
	}
	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return "", "", nil, err
	}
	return slug, path, cfg, nil
}

func gatewayPublicURL(gatewayURL, publicHost, label string) string {
	if strings.TrimSpace(label) == "" {
		return ""
	}
	host := strings.TrimSpace(publicHost)
	if host == "" {
		return ""
	}
	scheme := ""
	if strings.TrimSpace(gatewayURL) != "" {
		u, err := url.Parse(gatewayURL)
		if err == nil {
			scheme = u.Scheme
		}
	}
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s/", scheme, label, host)
}
