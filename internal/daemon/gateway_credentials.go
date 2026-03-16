package daemon

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"dev/internal/agent"
	"dev/internal/config"
)

const gatewayCredentialScanInterval = time.Minute

type gatewayCredentialTarget struct {
	GatewayURL    string
	CredentialDir string
}

type gatewayCredentialTracker struct {
	daemonCfg  *config.DaemonConfig
	scanEvery  time.Duration
	list       func(*config.DaemonConfig) ([]gatewayCredentialTarget, error)
	runRenewer func(context.Context, string, string) error

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func newGatewayCredentialTracker(daemonCfg *config.DaemonConfig) *gatewayCredentialTracker {
	return &gatewayCredentialTracker{
		daemonCfg:  daemonCfg,
		scanEvery:  gatewayCredentialScanInterval,
		list:       listGatewayCredentialTargets,
		runRenewer: agent.RunGatewayCredentialRenewer,
		active:     map[string]context.CancelFunc{},
	}
}

func (t *gatewayCredentialTracker) run(ctx context.Context) error {
	if err := t.sync(ctx); err != nil {
		log.Printf("daemon: gateway credential sync failed: %v", err)
	}
	ticker := time.NewTicker(t.scanEvery)
	defer ticker.Stop()
	defer t.stopAll()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := t.sync(ctx); err != nil {
				log.Printf("daemon: gateway credential sync failed: %v", err)
			}
		}
	}
}

func (t *gatewayCredentialTracker) sync(ctx context.Context) error {
	targets, err := t.list(t.daemonCfg)
	if err != nil {
		return err
	}
	seen := make(map[string]gatewayCredentialTarget, len(targets))
	for _, target := range targets {
		if target.CredentialDir == "" || target.GatewayURL == "" {
			continue
		}
		seen[target.CredentialDir] = target
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active == nil {
		t.active = map[string]context.CancelFunc{}
	}
	for dir, cancel := range t.active {
		if _, ok := seen[dir]; ok {
			continue
		}
		cancel()
		delete(t.active, dir)
	}
	for dir, target := range seen {
		if _, ok := t.active[dir]; ok {
			continue
		}
		renewCtx, cancel := context.WithCancel(ctx)
		t.active[dir] = cancel
		go func(target gatewayCredentialTarget) {
			if err := t.runRenewer(renewCtx, target.GatewayURL, target.CredentialDir); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("daemon: gateway credential renewer stopped for %s: %v", target.GatewayURL, err)
			}
		}(target)
	}
	return nil
}

func (t *gatewayCredentialTracker) stopAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for dir, cancel := range t.active {
		cancel()
		delete(t.active, dir)
	}
}

func listGatewayCredentialTargets(daemonCfg *config.DaemonConfig) ([]gatewayCredentialTarget, error) {
	stateDir, err := config.ResolveStateDir(daemonCfg)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(stateDir, "gateway", "agent-credentials")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	targets := make([]gatewayCredentialTarget, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		gatewayURL, err := config.LoadGatewayCredentialGatewayURL(dir)
		if err != nil {
			log.Printf("daemon: skipping gateway credentials in %s: %v", dir, err)
			continue
		}
		targets = append(targets, gatewayCredentialTarget{
			GatewayURL:    gatewayURL,
			CredentialDir: dir,
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].CredentialDir < targets[j].CredentialDir
	})
	return targets, nil
}
