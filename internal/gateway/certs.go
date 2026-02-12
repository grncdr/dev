package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/route53"
)

type CertProvisioner interface {
	EnsureLabel(ctx context.Context, label string) error
}

// ACMEOptions configures the ACME certificate manager.
type ACMEOptions struct {
	// PublicHost is the gateway's public hostname (used for wildcard certs).
	PublicHost string
	// Email is the ACME account contact email.
	Email string
	// DirectoryURL overrides the ACME CA directory (defaults to Let's Encrypt).
	DirectoryURL string
	// StorageDir is the filesystem path for certmagic certificate storage.
	StorageDir string
	// HostedZoneID is the Route53 hosted zone for DNS-01 validation.
	HostedZoneID string
	// Resolvers lists DNS resolvers used to verify TXT record propagation.
	Resolvers []string
}

// ACMEManager provisions wildcard TLS certificates for gateway tunnel labels
// using ACME DNS-01 challenges via certmagic.
type ACMEManager struct {
	cfg        *certmagic.Config
	publicHost string
	mu         sync.Mutex
	ensured    map[string]bool
	failed     map[string]time.Time
}

func NewACMEManager(ctx context.Context, opts ACMEOptions) (*ACMEManager, *tls.Config, error) {
	if strings.TrimSpace(opts.PublicHost) == "" {
		return nil, nil, errors.New("acme public host is required")
	}
	if strings.TrimSpace(opts.Email) == "" {
		return nil, nil, errors.New("acme email is required")
	}
	if strings.TrimSpace(opts.StorageDir) == "" {
		return nil, nil, errors.New("acme storage dir is required")
	}
	resolvers := normalizeResolvers(opts.Resolvers)
	certmagic.Default.Storage = &certmagic.FileStorage{Path: filepath.Clean(opts.StorageDir)}
	var cfg *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			return cfg, nil
		},
	})
	cfg = certmagic.New(cache, certmagic.Config{})
	cfg.Issuers = []certmagic.Issuer{
		certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			CA:                      defaultACMEDirectory(opts.DirectoryURL),
			Email:                   opts.Email,
			Agreed:                  true,
			DisableHTTPChallenge:    true,
			DisableTLSALPNChallenge: true,
			DNS01Solver: &certmagic.DNS01Solver{
				DNSManager: certmagic.DNSManager{
					DNSProvider: &route53.Provider{
						HostedZoneID: opts.HostedZoneID,
					},
					Resolvers: resolvers,
					// PropagationDelay adds wait time after our Resolvers confirm the
					// TXT record exists, before notifying Let's Encrypt to validate.
					// This allows time for the record to propagate to Let's Encrypt's
					// DNS infrastructure, avoiding validation failures that would
					// otherwise trigger certmagic's retry loop.
					PropagationDelay: 10 * time.Second,
				},
			},
		}),
	}

	publicHost := normalizeHost(opts.PublicHost)
	baseWildcard := "*." + publicHost
	if err := cfg.ManageSync(ctx, []string{baseWildcard}); err != nil {
		return nil, nil, fmt.Errorf("provision base wildcard cert %s: %w", baseWildcard, err)
	}

	manager := &ACMEManager{
		cfg:        cfg,
		publicHost: publicHost,
		ensured:    map[string]bool{baseWildcard: true},
		failed:     map[string]time.Time{},
	}
	tlsConfig := cfg.TLSConfig()
	tlsConfig.NextProtos = ensureALPN(tlsConfig.NextProtos)
	return manager, tlsConfig, nil
}

func (m *ACMEManager) EnsureLabel(ctx context.Context, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return errors.New("label is required")
	}
	name := "*." + label + "." + m.publicHost
	m.mu.Lock()
	if m.ensured[name] {
		m.mu.Unlock()
		return nil
	}
	if until, ok := m.failed[name]; ok {
		if time.Now().Before(until) {
			m.mu.Unlock()
			return fmt.Errorf("acme retry suppressed for %s until %s", name, until.UTC().Format(time.RFC3339))
		}
	}
	m.mu.Unlock()
	if err := m.cfg.ManageSync(ctx, []string{name}); err != nil {
		now := time.Now().UTC()
		retryAt := nextACMERetry(err, now)
		if retryAt.After(now) {
			m.mu.Lock()
			m.failed[name] = retryAt
			m.mu.Unlock()
			return fmt.Errorf("provision wildcard cert %s: %w (next retry after %s)", name, err, retryAt.Format(time.RFC3339))
		}
		m.mu.Lock()
		delete(m.failed, name)
		m.mu.Unlock()
		return fmt.Errorf("provision wildcard cert %s: %w", name, err)
	}
	m.mu.Lock()
	m.ensured[name] = true
	delete(m.failed, name)
	m.mu.Unlock()
	return nil
}

func defaultACMEDirectory(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		return dir
	}
	return "https://acme-v02.api.letsencrypt.org/directory"
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, ".")
	host = strings.TrimSuffix(host, ".")
	return host
}

func normalizeResolvers(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		resolver := strings.TrimSpace(value)
		if resolver == "" || seen[resolver] {
			continue
		}
		seen[resolver] = true
		out = append(out, resolver)
	}
	if len(out) == 0 {
		return []string{"1.1.1.1"}
	}
	return out
}

func ensureALPN(existing []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+2)
	for _, proto := range existing {
		if strings.TrimSpace(proto) == "" || seen[proto] {
			continue
		}
		seen[proto] = true
		out = append(out, proto)
	}
	for _, proto := range []string{"h2", "http/1.1"} {
		if !seen[proto] {
			out = append(out, proto)
		}
	}
	return out
}

var acmeRetryAfterRE = regexp.MustCompile(`retry after ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} UTC)`)

func nextACMERetry(err error, now time.Time) time.Time {
	if err != nil {
		msg := err.Error()
		lower := strings.ToLower(msg)
		// Cancellation/interruption failures are transient and should be retried
		// immediately instead of suppressing retries for 15 minutes.
		if strings.Contains(lower, "context canceled") || strings.Contains(lower, "context deadline exceeded") {
			return now
		}
		matches := acmeRetryAfterRE.FindStringSubmatch(msg)
		if len(matches) == 2 {
			if ts, parseErr := time.Parse("2006-01-02 15:04:05 MST", matches[1]); parseErr == nil && ts.After(now) {
				return ts
			}
		}
	}
	return now.Add(15 * time.Minute)
}
