package agent

import (
	"context"
	"log"
	"time"

	"dev/internal/config"
)

const (
	gatewayCredentialRenewBefore = 48 * time.Hour
	gatewayCredentialRetryEvery  = 10 * time.Minute
)

type gatewayCredentialRenewer struct {
	gatewayURL    string
	credentialDir string
	renewBefore   time.Duration
	retryEvery    time.Duration
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	renew         func(string, string) (time.Time, error)
}

func newGatewayCredentialRenewer(gatewayURL, credentialDir string) *gatewayCredentialRenewer {
	return &gatewayCredentialRenewer{
		gatewayURL:    gatewayURL,
		credentialDir: credentialDir,
		renewBefore:   gatewayCredentialRenewBefore,
		retryEvery:    gatewayCredentialRetryEvery,
		now:           time.Now,
		sleep:         sleepWithContext,
		renew:         RenewGatewayCredentials,
	}
}

func RunGatewayCredentialRenewer(ctx context.Context, gatewayURL, credentialDir string) error {
	return newGatewayCredentialRenewer(gatewayURL, credentialDir).run(ctx)
}

func (r *gatewayCredentialRenewer) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := config.LoadGatewayCredentialInfo(r.credentialDir)
		if err != nil {
			return err
		}
		expiresAt := info.ExpiresAt
		now := r.now()
		if !expiresAt.After(now) {
			return nil
		}
		renewAt := expiresAt.Add(-r.renewBefore)
		if renewAt.After(now) {
			if err := r.sleep(ctx, renewAt.Sub(now)); err != nil {
				return err
			}
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			now = r.now()
			if !expiresAt.After(now) {
				return nil
			}
			newExpiry, err := r.renew(r.gatewayURL, r.credentialDir)
			if err == nil {
				log.Printf("agent: renewed gateway client certificate for %s until %s", r.gatewayURL, newExpiry.UTC().Format(time.RFC3339))
				break
			}
			log.Printf("agent: gateway client certificate renewal failed for %s: %v", r.gatewayURL, err)
			wait := r.retryEvery
			remaining := expiresAt.Sub(now)
			if remaining < wait {
				wait = remaining
			}
			if wait <= 0 {
				return nil
			}
			if err := r.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
