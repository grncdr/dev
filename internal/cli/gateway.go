package cli

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dev/internal/config"
	"dev/internal/gateway"
)

func newGatewayCmd(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "manage the gateway",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "run the gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGateway(opts)
		},
	})
	cmd.AddCommand(newGatewayInitCmd(opts))
	cmd.AddCommand(newGatewayInviteCmd(opts))
	cmd.AddCommand(newGatewayLoginCmd(opts))
	return cmd
}

func newGatewayInitCmd(opts *Options) *cobra.Command {
	var ttl time.Duration
	var uses int
	cmd := &cobra.Command{
		Use:   "init",
		Short: "initialize gateway state and print a bootstrap invite code",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGatewayInit(opts, ttl, uses)
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 5*time.Minute, "invite TTL")
	cmd.Flags().IntVar(&uses, "uses", 1, "number of uses")
	return cmd
}

func newGatewayLoginCmd(opts *Options) *cobra.Command {
	var name string
	var gatewayURL string
	cmd := &cobra.Command{
		Use:   "login <invite-code>",
		Short: "exchange invite code for gateway agent credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGatewayLogin(opts, args[0], name, gatewayURL)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name (defaults to $USER)")
	cmd.Flags().StringVar(&gatewayURL, "gateway-url", "", "gateway URL (defaults to project gateway.url)")
	return cmd
}

func runGateway(opts *Options) error {
	daemonCfg, _, err := config.LoadDaemonConfig(opts.ResolvedPaths.DaemonConfig)
	if err != nil {
		return err
	}
	dataDir, err := config.ResolveGatewayDataDir(daemonCfg)
	if err != nil {
		return err
	}

	listen := daemonCfg.Gateway.Listen
	if listen == "" {
		listen = ":443"
	}
	dnsZone := strings.TrimSpace(daemonCfg.Gateway.DNSZone)
	if dnsZone == "" {
		return errors.New("gateway.dns_zone is required")
	}

	dnsProvider, err := buildGatewayDNSProvider(daemonCfg)
	if err != nil {
		return err
	}

	certProvisioner, tlsConfig, err := buildGatewayACME(context.Background(), daemonCfg, dataDir)
	if err != nil {
		return err
	}

	srv, err := gateway.NewServer(gateway.ServerOptions{
		ListenAddr: listen,
		DataDir:    dataDir,
		DNSZone:    dnsZone,
		Auth:       daemonCfg.Gateway.Auth,
		DNS:        dnsProvider,
		Certs:      certProvisioner,
		TLSConfig:  tlsConfig,
	})
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve()
	}()

	fmt.Printf("gateway listening on %s (data dir: %s)\n", srv.Addr(), dataDir)
	if daemonCfg.Gateway.Auth.Enabled {
		fmt.Println("gateway basic auth enabled")
	}
	if dnsProvider != nil {
		fmt.Println("gateway Route53 DNS sync enabled")
	}
	if certProvisioner != nil {
		fmt.Println("gateway ACME wildcard TLS enabled")
	}
	if tlsConfig == nil && listenLooksTLS(listen) {
		return fmt.Errorf("refusing to listen on %s without TLS; configure ACME (gateway.dns_zone, gateway.acme_email, gateway.route53.*) or use a non-TLS listen port", listen)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
		fmt.Printf("gateway stopped (%s)\n", sig.String())
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func buildGatewayDNSProvider(daemonCfg *config.DaemonConfig) (gateway.DNSProvider, error) {
	if daemonCfg == nil || !daemonCfg.Gateway.Route53.Enabled {
		return nil, nil
	}
	dnsZone := strings.TrimSpace(daemonCfg.Gateway.DNSZone)
	if dnsZone == "" {
		return nil, errors.New("gateway.dns_zone is required when gateway.route53.enabled=true")
	}
	dnsZone = strings.TrimSuffix(dnsZone, ".")
	hostname := strings.TrimSpace(daemonCfg.Gateway.Hostname)
	if hostname == "" {
		return nil, errors.New("gateway.hostname is required when gateway.route53.enabled=true")
	}
	hostname = strings.TrimSuffix(hostname, ".")
	opts := gateway.Route53Options{
		HostedZoneID: daemonCfg.Gateway.Route53.HostedZoneID,
		Domain:       dnsZone,
		Target:       hostname,
		TTL:          daemonCfg.Gateway.Route53.TTL,
	}
	if _, err := url.Parse("https://" + dnsZone); err != nil {
		return nil, fmt.Errorf("invalid gateway.dns_zone: %w", err)
	}
	if _, err := url.Parse("https://" + hostname); err != nil {
		return nil, fmt.Errorf("invalid gateway.hostname: %w", err)
	}
	return gateway.NewRoute53Provider(context.Background(), opts)
}

func buildGatewayACME(ctx context.Context, daemonCfg *config.DaemonConfig, dataDir string) (gateway.CertProvisioner, *tls.Config, error) {
	if daemonCfg == nil {
		return nil, nil, nil
	}
	if !daemonCfg.Gateway.Route53.Enabled {
		return nil, nil, nil
	}
	dnsZone := strings.TrimSpace(daemonCfg.Gateway.DNSZone)
	if dnsZone == "" {
		return nil, nil, errors.New("gateway.dns_zone is required for ACME wildcard certificates")
	}
	email := strings.TrimSpace(daemonCfg.Gateway.ACMEEmail)
	if email == "" {
		return nil, nil, errors.New("gateway.acme_email is required for ACME wildcard certificates")
	}
	store := strings.TrimSpace(daemonCfg.Gateway.ACMEStore)
	if store == "" {
		store = dataDir // certmagic adds its own "acme/" subdirectory
	}
	storePath, err := config.ExpandUserPath(store)
	if err != nil {
		return nil, nil, err
	}
	manager, tlsConfig, err := gateway.NewACMEManager(ctx, gateway.ACMEOptions{
		PublicHost:   dnsZone,
		Email:        email,
		DirectoryURL: daemonCfg.Gateway.ACMEDir,
		StorageDir:   storePath,
		HostedZoneID: daemonCfg.Gateway.Route53.HostedZoneID,
		Resolvers:    daemonCfg.Gateway.ACMEResolvers,
	})
	if err != nil {
		return nil, nil, err
	}
	return manager, tlsConfig, nil
}

func runGatewayInit(opts *Options, ttl time.Duration, uses int) error {
	daemonCfg, err := loadDaemonConfig(opts)
	if err != nil {
		return err
	}
	dataDir, err := config.ResolveGatewayDataDir(daemonCfg)
	if err != nil {
		return err
	}
	store, err := gateway.NewInviteStore(dataDir)
	if err != nil {
		return err
	}
	invite, err := store.Create(ttl, uses)
	if err != nil {
		return err
	}
	fmt.Printf("gateway state initialized at %s\n", dataDir)
	fmt.Printf("bootstrap invite code: %s\n", invite.Code)
	fmt.Printf("expires at: %s\n", invite.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("uses: %d\n", invite.UsesLeft)
	return nil
}

func runGatewayLogin(opts *Options, inviteCode, name, gatewayURL string) error {
	if strings.TrimSpace(name) == "" {
		name = os.Getenv("USER")
		if name == "" {
			name = "dev-user"
		}
	}
	gatewayURL = resolveGatewayURLForLogin(opts, gatewayURL)
	if gatewayURL == "" {
		return errors.New("gateway URL is required (use --gateway-url or set gateway.url in project config)")
	}

	key, csrPEM, err := generateGatewayCSR(name)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"invite_code": inviteCode,
		"name":        name,
		"csr":         string(csrPEM),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(gatewayURL, "/") + "/_agent/cert/issue"
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gateway login failed: %s (%s)", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out struct {
		CertPEM string `json:"cert_pem"`
		CAPEM   string `json:"ca_pem"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return err
	}
	if out.CertPEM == "" || out.CAPEM == "" {
		return errors.New("gateway returned empty certificate material")
	}
	credDir, err := config.ResolveGatewayCredentialDirForURL(nil, gatewayURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyPath := filepath.Join(credDir, "client-key.pem")
	certPath := filepath.Join(credDir, "client.pem")
	caPath := filepath.Join(credDir, "ca.pem")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certPath, []byte(out.CertPEM), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(caPath, []byte(out.CAPEM), 0o600); err != nil {
		return err
	}
	fmt.Printf("gateway login succeeded for %s\n", name)
	fmt.Printf("credentials written to %s\n", credDir)
	return nil
}

func resolveGatewayURLForLogin(opts *Options, explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit
	}
	if opts == nil || opts.ResolvedPaths.ProjectConfig == "" {
		return ""
	}
	projectCfg, _, err := config.LoadProjectConfig(opts.ResolvedPaths.ProjectConfig)
	if err != nil || projectCfg == nil {
		return ""
	}
	return config.ProjectGatewayURL(projectCfg)
}

func generateGatewayCSR(name string) (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: name},
	}, key)
	if err != nil {
		return nil, nil, err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return key, csrPEM, nil
}

func listenLooksTLS(listen string) bool {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return false
	}
	if strings.HasSuffix(listen, ":443") || listen == "443" || listen == ":https" {
		return true
	}
	return false
}
