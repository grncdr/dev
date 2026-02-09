package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/gateway"
)

func newGatewayInviteCmd(opts *Options) *cobra.Command {
	var ttl time.Duration
	var uses int
	var gatewayURL string
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "create an invite code",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInviteCreate(opts, gatewayURL, ttl, uses)
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 5*time.Minute, "invite TTL")
	cmd.Flags().IntVar(&uses, "uses", 1, "number of uses")
	cmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "gateway URL (defaults to project gateway.url)")
	return cmd
}

func runInviteCreate(opts *Options, gatewayURL string, ttl time.Duration, uses int) error {
	gatewayURL = resolveGatewayURLForLogin(opts, gatewayURL)
	if gatewayURL == "" {
		return errors.New("gateway URL is required (use --gateway-url or set gateway.url in project config)")
	}

	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}

	client, _, err := gateway.MTLSClientForGatewayURL(gatewayURL, daemonCfg)
	if err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	baseURL, err := url.Parse(gatewayURL)
	if err != nil {
		return err
	}
	endpoint := baseURL.ResolveReference(&url.URL{Path: "/_agent/invites/create"})

	payload := map[string]any{
		"ttl_seconds": int64(ttl / time.Second),
		"uses":        uses,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("invite create failed: %s (%s)", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		InviteCode string    `json:"invite_code"`
		ExpiresAt  time.Time `json:"expires_at"`
		Uses       int       `json:"uses"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return err
	}
	if strings.TrimSpace(out.InviteCode) == "" {
		return errors.New("gateway returned empty invite code")
	}
	fmt.Printf("invite code: %s\n", out.InviteCode)
	fmt.Printf("expires at: %s\n", out.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("uses: %d\n", out.Uses)
	return nil
}
