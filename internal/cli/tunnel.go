package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev-mode/internal/config"
	"dev-mode/internal/daemon"
	"dev-mode/internal/worktree"
)

func newTunnelCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "manage gateway tunnels",
	}
	cmd.AddCommand(newTunnelOpenCmd(opts))
	cmd.AddCommand(newTunnelCloseCmd(opts))
	cmd.AddCommand(newTunnelStatusCmd(opts))
	return cmd
}

func newTunnelOpenCmd(opts *Options) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "open [slug]",
		Short: "open a tunnel for a worktree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugArg string
			if len(args) > 0 {
				slugArg = args[0]
			}
			return runTunnelOpen(opts, slugArg, label)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "override tunnel label (defaults to slug)")
	return cmd
}

func newTunnelCloseCmd(opts *Options) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "close [slug]",
		Short: "close a tunnel for a worktree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugArg string
			if len(args) > 0 {
				slugArg = args[0]
			}
			return runTunnelClose(opts, slugArg, label)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "close by label")
	return cmd
}

func newTunnelStatusCmd(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "show tunnel status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTunnelStatus(opts)
		},
	}
}

func runTunnelOpen(opts *Options, slugArg, labelArg string) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}

	slug, cfg, err := resolveTunnelConfig(slugArg)
	if err != nil {
		return err
	}
	gatewayURL, _ := cfg.Gateway["url"].(string)
	gatewayURL = strings.TrimSpace(gatewayURL)
	if gatewayURL == "" {
		return errors.New("gateway.url is required in project config")
	}
	label := strings.TrimSpace(labelArg)
	if label == "" {
		label = effectiveDisplaySlug(slug, cfg)
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
	if publicURL := gatewayPublicURL(gatewayURL, label); publicURL != "" {
		fmt.Printf("public base URL: %s\n", publicURL)
	}
	return nil
}

func runTunnelClose(opts *Options, slugArg, labelArg string) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if err := ensureDaemonRunning(opts, socketPath); err != nil {
		return err
	}
	req := daemon.TunnelRequest{Label: strings.TrimSpace(labelArg)}
	if req.Label == "" {
		slug, cfg, err := resolveTunnelConfig(slugArg)
		if err != nil {
			return err
		}
		req.Slug = slug
		req.Label = effectiveDisplaySlug(slug, cfg)
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

func runTunnelStatus(opts *Options) error {
	socketPath, err := daemon.ResolveSocketPath()
	if err != nil {
		return err
	}
	if running, err := daemonRunning(socketPath); err != nil {
		return err
	} else if !running {
		fmt.Println("daemon not running")
		return nil
	}
	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.TunnelsStatus(ctx)
	if err != nil {
		return err
	}
	if len(resp.Tunnels) == 0 {
		fmt.Println("no active tunnels")
		return nil
	}
	sort.Slice(resp.Tunnels, func(i, j int) bool {
		return resp.Tunnels[i].Label < resp.Tunnels[j].Label
	})
	for _, t := range resp.Tunnels {
		fmt.Printf("%s (%s): %s\n", t.Label, t.Slug, t.Status)
		if publicURL := gatewayPublicURL(t.GatewayURL, t.Label); publicURL != "" {
			fmt.Printf("  %s\n", publicURL)
		}
		if t.LastError != "" {
			fmt.Printf("  error: %s\n", t.LastError)
		}
	}
	return nil
}

func resolveTunnelConfig(slugArg string) (string, *config.ProjectConfig, error) {
	slug, err := resolveSlug(slugArg)
	if err != nil {
		return "", nil, err
	}
	path, err := worktree.ResolvePathFromSlug(slug)
	if err != nil {
		return "", nil, err
	}
	cfgPath := filepath.Join(path, config.DefaultProjectConfig)
	cfg, _, err := config.LoadProjectConfig(cfgPath)
	if err != nil {
		return "", nil, err
	}
	return slug, cfg, nil
}

func gatewayPublicURL(gatewayURL, label string) string {
	if strings.TrimSpace(gatewayURL) == "" || strings.TrimSpace(label) == "" {
		return ""
	}
	u, err := url.Parse(gatewayURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s.%s/", scheme, label, host)
}
