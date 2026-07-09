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
	var auth string
	var noAuth bool
	cmd := &cobra.Command{
		Use:   "share [slug]",
		Short: "share a worktree through the gateway",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var slugArg string
			if len(args) > 0 {
				slugArg = args[0]
			}
			return runTunnelOpen(opts, slugArg, label, gatewayURL, auth, noAuth)
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "override tunnel label (defaults to slug)")
	cmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "gateway URL override")
	cmd.Flags().StringVar(&auth, "auth", "", "basic auth credentials for shared gateway access in username:password format")
	cmd.Flags().BoolVar(&noAuth, "no-auth", false, "disable project gateway.auth defaults for this share")
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

func runTunnelOpen(opts *Options, slugArg, labelArg, gatewayURLArg, authArg string, noAuth bool) error {
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
		gatewayURL = config.ProjectGatewayURL(cfg)
	}
	if gatewayURL == "" {
		return errors.New("gateway.url is required in project config or pass --gateway-url")
	}
	label := strings.TrimSpace(labelArg)
	if label == "" {
		label = effectiveDisplaySlug(slug, path, cfg)
	}
	authUsername, authPassword, err := resolveShareAuth(cfg, authArg, noAuth)
	if err != nil {
		return err
	}

	req := daemon.TunnelRequest{
		Identifier:   worktree.Identifier{Project: cfg.Project.Name, Slug: slug},
		Path:         path,
		Label:        label,
		GatewayURL:   gatewayURL,
		Name:         os.Getenv("USER"),
		AuthUsername: authUsername,
		AuthPassword: authPassword,
	}

	client := daemon.NewClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial, err := client.TunnelOpen(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("tunnel %s (%s): %s\n", initial.Label, initial.Slug, initial.Status)
	if publicURL := gatewayPublicURL(gatewayURL, initial.PublicHost, label); publicURL != "" {
		fmt.Printf("public base URL: %s\n", publicURL)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer waitCancel()
	last := daemon.TunnelStatus{
		Status:          initial.Status,
		RegisterStage:   initial.RegisterStage,
		RegisterMessage: initial.RegisterMessage,
		PublicHost:      initial.PublicHost,
		LastError:       initial.LastError,
	}
	seenProgress := map[string]bool{}
	for {
		status, err := tunnelStatusForLabel(waitCtx, client, label)
		if err != nil {
			return err
		}
		printTunnelProgress(last, *status, seenProgress)
		last = *status
		if status.Status == "connected" {
			if publicURL := gatewayPublicURL(gatewayURL, status.PublicHost, label); publicURL != "" {
				fmt.Printf("public base URL: %s\n", publicURL)
			}
			return nil
		}
		if status.Status == "error" {
			if strings.TrimSpace(status.LastError) != "" {
				return errors.New(status.LastError)
			}
			return errors.New("tunnel failed")
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for tunnel %s to connect", label)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func resolveShareAuth(cfg *config.ProjectConfig, authArg string, noAuth bool) (username string, password string, err error) {
	if noAuth && strings.TrimSpace(authArg) != "" {
		return "", "", errors.New("--auth and --no-auth cannot be used together")
	}
	if strings.TrimSpace(authArg) != "" {
		return parseShareAuthArg(authArg)
	}
	if noAuth {
		return "", "", nil
	}
	creds, enabled, err := config.ProjectGatewayAuthCredentials(cfg)
	if err != nil {
		return "", "", err
	}
	if !enabled {
		return "", "", nil
	}
	return creds.Username, creds.Password, nil
}

func parseShareAuthArg(raw string) (username string, password string, err error) {
	raw = strings.TrimSpace(raw)
	user, pass, ok := strings.Cut(raw, ":")
	if !ok {
		return "", "", errors.New("--auth must be in username:password format")
	}
	user = strings.TrimSpace(user)
	if user == "" || pass == "" {
		return "", "", errors.New("--auth requires both username and password")
	}
	return user, pass, nil
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
	if req.Label == "" || strings.TrimSpace(gatewayURLArg) == "" {
		slug, _, cfg, err := resolveTunnelConfig(opts, slugArg)
		if err != nil {
			return err
		}
		if strings.TrimSpace(gatewayURLArg) == "" && config.ProjectGatewayURL(cfg) == "" {
			return errors.New("gateway.url is required in project config or pass --gateway-url")
		}
		if req.Label == "" {
			req.Slug = slug
			req.Project = cfg.Project.Name
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

func tunnelStatusForLabel(ctx context.Context, client *daemon.Client, label string) (*daemon.TunnelStatus, error) {
	all, err := client.TunnelsStatus(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all.Tunnels {
		if all.Tunnels[i].Label == label {
			return &all.Tunnels[i], nil
		}
	}
	return nil, fmt.Errorf("tunnel %s is no longer active", label)
}

func printTunnelProgress(last, cur daemon.TunnelStatus, seenProgress map[string]bool) {
	if cur.RegisterStage != "" && (cur.RegisterStage != last.RegisterStage || cur.RegisterMessage != last.RegisterMessage) {
		key := cur.RegisterStage + "|" + cur.RegisterMessage
		if !seenProgress[key] {
			seenProgress[key] = true
			if cur.RegisterMessage != "" {
				fmt.Printf("provisioning[%s]: %s\n", cur.RegisterStage, cur.RegisterMessage)
			} else {
				fmt.Printf("provisioning[%s]\n", cur.RegisterStage)
			}
		}
	}
	if cur.LastError != "" && cur.LastError != last.LastError {
		fmt.Printf("provisioning retry: %s\n", cur.LastError)
	}
	if cur.Status != last.Status {
		fmt.Printf("tunnel status: %s\n", cur.Status)
	}
}
