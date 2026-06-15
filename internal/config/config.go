package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultProjectConfig = ".dev.toml"
	DefaultLocalOverride = ".dev.local.toml"
	DefaultDaemonConfig  = "~/.config/dev/daemon.toml"
)

// ProjectConfig is the parsed representation of a .dev.toml project file.
type ProjectConfig struct {
	// Project holds the [project] table (name, main_slug).
	Project ProjectBlock `toml:"project" json:"project"`
	// Gateway holds the raw [gateway] table (url, expose, auth).
	// Parsed on demand via helpers like GatewayExposeRules.
	Gateway map[string]any `toml:"gateway" json:"gateway,omitempty"`
	// Processes maps process names to their raw [process.<name>] tables.
	Processes map[string]map[string]any `toml:"process" json:"processes,omitempty"`
	// Proxy holds the [proxy] table (reserved for future use).
	Proxy ProjectProxyBlock `toml:"proxy" json:"proxy,omitempty"`
	// LocalDNS holds the [local-dns] table (hostname overrides).
	LocalDNS ProjectLocalDNSBlock `toml:"local-dns" json:"local_dns,omitempty"`
	// Hooks holds the [hooks] table (lifecycle shell commands).
	Hooks HooksBlock `toml:"hooks" json:"hooks,omitempty"`
	// Commands holds the [commands] table (wrapper configuration).
	Commands CommandsBlock `toml:"commands" json:"commands,omitempty"`
	// DefaultSubdomain is the subdomain to redirect to when a request hits
	// the worktree's base host (e.g. <slug>.<apex>) and no proxy matcher
	// matches. The value is a literal DNS label, not a process name.
	DefaultSubdomain string `toml:"default_subdomain" json:"default_subdomain,omitempty"`
}

// ProjectBlock holds the [project] table in a .dev.toml file.
type ProjectBlock struct {
	// Name is the project identifier (required).
	Name string `toml:"name" json:"name"`
	// MainSlug overrides the default "main" slug for the primary worktree.
	// Affects DNS label resolution via worktree.ProxyDNSLabelForSlug.
	MainSlug string `toml:"main_slug" json:"main_slug,omitempty"`
}

// DaemonConfig is the parsed representation of the user's daemon.toml file.
type DaemonConfig struct {
	// StateDir overrides the default state directory (~/.local/state/dev).
	StateDir string `toml:"state_dir" json:"state_dir,omitempty"`
	// Gateway holds the [gateway] table for running a gateway server.
	Gateway DaemonGatewayBlock `toml:"gateway" json:"gateway,omitempty"`
	// LocalProxy holds the [local-proxy] table for the local HTTPS proxy.
	LocalProxy DaemonLocalProxyBlock `toml:"local-proxy" json:"local_proxy,omitempty"`
	// WorktreeDir overrides the default worktree checkout directory.
	WorktreeDir string `toml:"worktree_dir" json:"worktree_dir,omitempty"`
	// GlobalHooks holds lifecycle hooks that apply to all projects.
	GlobalHooks HooksBlock `toml:"global-hooks" json:"global_hooks,omitempty"`
	// ProjectHooks maps project names to per-project lifecycle hooks.
	ProjectHooks map[string]HooksBlock `toml:"project-hooks" json:"project_hooks,omitempty"`
}

// DaemonGatewayBlock holds the [gateway] table in daemon.toml for running
// a gateway server that exposes tunnels to the public internet.
// Its fields drive gateway.ServerOptions and gateway.ACMEOptions.
type DaemonGatewayBlock struct {
	// Enabled controls whether the gateway server starts.
	Enabled bool `toml:"enabled" json:"enabled,omitempty"`
	// Listen is the HTTPS listen address (e.g. ":443").
	Listen string `toml:"listen" json:"listen,omitempty"`
	// HTTPListen is the optional plaintext HTTP listen address.
	HTTPListen string `toml:"http_listen" json:"http_listen,omitempty"`
	// DNSZone is the public DNS zone for tunnel subdomains.
	DNSZone string `toml:"dns_zone" json:"dns_zone,omitempty"`
	// Hostname is the gateway's public hostname.
	Hostname string `toml:"hostname" json:"hostname,omitempty"`
	// ACMEEmail is the contact email for ACME certificate issuance.
	ACMEEmail string `toml:"acme_email" json:"acme_email,omitempty"`
	// ACMEDir overrides the ACME directory URL (defaults to Let's Encrypt).
	ACMEDir string `toml:"acme_directory" json:"acme_directory,omitempty"`
	// ACMEStore is the filesystem path for ACME certificate storage.
	ACMEStore string `toml:"acme_storage" json:"acme_storage,omitempty"`
	// ACMEResolvers lists DNS resolvers for ACME DNS-01 validation.
	ACMEResolvers []string `toml:"acme_resolvers" json:"acme_resolvers,omitempty"`
	// Auth holds HTTP basic auth credentials for agent authentication.
	Auth DaemonGatewayAuth `toml:"auth" json:"auth,omitempty"`
	// Route53 holds AWS Route53 DNS provisioning settings.
	Route53 DaemonGatewayRoute53 `toml:"route53" json:"route53,omitempty"`
}

// DaemonGatewayAuth holds HTTP basic auth credentials for the gateway's agent API.
type DaemonGatewayAuth struct {
	Enabled  bool   `toml:"enabled" json:"enabled,omitempty"`
	Username string `toml:"username" json:"username,omitempty"`
	Password string `toml:"password" json:"password,omitempty"`
}

// DaemonGatewayRoute53 holds AWS Route53 DNS settings for automatic tunnel DNS provisioning.
type DaemonGatewayRoute53 struct {
	Enabled      bool   `toml:"enabled" json:"enabled,omitempty"`
	HostedZoneID string `toml:"hosted_zone_id" json:"hosted_zone_id,omitempty"`
	TTL          int64  `toml:"ttl" json:"ttl,omitempty"`
}

// HooksBlock holds shell commands executed at worktree and process lifecycle events.
type HooksBlock struct {
	PreWorktreeAdd      string `toml:"pre_worktree_add" json:"pre_worktree_add,omitempty"`
	PostWorktreeAdd     string `toml:"post_worktree_add" json:"post_worktree_add,omitempty"`
	PreWorktreeCleanup  string `toml:"pre_worktree_cleanup" json:"pre_worktree_cleanup,omitempty"`
	PostWorktreeCleanup string `toml:"post_worktree_cleanup" json:"post_worktree_cleanup,omitempty"`
	PreStart            string `toml:"pre_start" json:"pre_start,omitempty"`
	PostStart           string `toml:"post_start" json:"post_start,omitempty"`
	PreStop             string `toml:"pre_stop" json:"pre_stop,omitempty"`
	PostStop            string `toml:"post_stop" json:"post_stop,omitempty"`
}

// CommandsBlock holds the [commands] table, currently only the optional shell
// wrapper prefix (e.g. "mise exec --" or "direnv exec .").
type CommandsBlock struct {
	// Wrapper is prepended to all process and hook commands.
	Wrapper string `toml:"wrapper" json:"wrapper,omitempty"`
}

// ProjectProxyBlock holds the [proxy] table (reserved for future use).
type ProjectProxyBlock struct {
}

// ProjectLocalDNSBlock holds the [local-dns] table for per-project hostname overrides.
type ProjectLocalDNSBlock struct {
	// Overrides maps worktree slugs to custom DNS labels for the local proxy.
	Overrides map[string]string `toml:"overrides" json:"overrides,omitempty"`
}

// DaemonLocalProxyBlock holds the [local-proxy] table in daemon.toml for the
// local HTTPS proxy that routes <slug>.<apex> hostnames to running processes.
type DaemonLocalProxyBlock struct {
	// Enabled controls whether the local proxy starts (defaults to true).
	Enabled *bool `toml:"enabled" json:"enabled,omitempty"`
	// ListenHTTP is the HTTP listen address (e.g. "127.0.0.1:80").
	ListenHTTP string `toml:"listen_http" json:"listen_http,omitempty"`
	// ListenHTTPS is the HTTPS listen address (e.g. "127.0.0.1:443").
	ListenHTTPS string `toml:"listen_https" json:"listen_https,omitempty"`
	// ApexZone is the DNS suffix for local routing (defaults to ".localhost").
	ApexZone string `toml:"apex_zone" json:"apex_zone,omitempty"`
	// Allow is a CIDR range of allowed client IPs.
	Allow string `toml:"allow" json:"allow,omitempty"`
}

func (b *DaemonLocalProxyBlock) IsEnabled() bool {
	if b.Enabled == nil {
		return true // default enabled
	}
	return *b.Enabled
}

// LoadInfo records which config files were found and used during loading.
type LoadInfo struct {
	// ConfigPath is the primary .dev.toml path that was loaded.
	ConfigPath string
	// LocalOverridePath is the path checked for .dev.local.toml.
	LocalOverridePath string
	// LocalOverrideUsed is true when .dev.local.toml was found and merged.
	LocalOverrideUsed bool
	// DaemonConfigPath is the daemon.toml path that was checked.
	DaemonConfigPath string
	// DaemonConfigFound is true when daemon.toml existed on disk.
	DaemonConfigFound bool
}

func LoadProjectConfig(configPath string) (*ProjectConfig, *LoadInfo, error) {
	info := &LoadInfo{ConfigPath: configPath}

	baseMap, err := loadTOMLMap(configPath)
	if err != nil {
		return nil, info, err
	}

	localOverride := filepath.Join(filepath.Dir(configPath), DefaultLocalOverride)
	info.LocalOverridePath = localOverride
	if _, err := os.Stat(localOverride); err == nil {
		overrideMap, err := loadTOMLMap(localOverride)
		if err != nil {
			return nil, info, err
		}
		mergeMaps(baseMap, overrideMap)
		info.LocalOverrideUsed = true
	}

	mergedBytes, err := toml.Marshal(baseMap)
	if err != nil {
		return nil, info, fmt.Errorf("marshal merged config: %w", err)
	}

	var cfg ProjectConfig
	if err := toml.Unmarshal(mergedBytes, &cfg); err != nil {
		return nil, info, fmt.Errorf("decode merged config: %w", err)
	}

	if err := validateProjectConfig(&cfg); err != nil {
		return nil, info, err
	}

	return &cfg, info, nil
}

func LoadDaemonConfig(daemonConfigPath string) (*DaemonConfig, *LoadInfo, error) {
	info := &LoadInfo{DaemonConfigPath: daemonConfigPath}

	if _, err := os.Stat(daemonConfigPath); errors.Is(err, os.ErrNotExist) {
		return &DaemonConfig{}, info, nil
	}

	data, err := os.ReadFile(daemonConfigPath)
	if err != nil {
		return nil, info, fmt.Errorf("read daemon config: %w", err)
	}

	var cfg DaemonConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, info, fmt.Errorf("decode daemon config: %w", err)
	}

	info.DaemonConfigFound = true
	return &cfg, info, nil
}

func ExpandUserPath(path string) (string, error) {
	if len(path) == 0 || path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}

func ExpandCommandPath(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	expanded, err := ExpandUserPath(args[0])
	if err != nil {
		return nil, err
	}
	clone := append([]string(nil), args...)
	clone[0] = expanded
	return clone, nil
}

func CollapseUserPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	cleanHome := filepath.Clean(strings.TrimSpace(home))
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanHome == "" {
		return path
	}
	if collapsed, ok := collapsePathForDisplay(cleanPath, cleanHome); ok {
		return collapsed
	}
	resolvedPath, pathErr := filepath.EvalSymlinks(cleanPath)
	resolvedHome, homeErr := filepath.EvalSymlinks(cleanHome)
	if pathErr != nil || homeErr != nil {
		return path
	}
	if collapsed, ok := collapsePathForDisplay(resolvedPath, resolvedHome); ok {
		return collapsed
	}
	return path
}

func collapsePathForDisplay(path, home string) (string, bool) {
	if path == home {
		return "~", true
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join("~", rel), true
}

const DefaultStateDir = "~/.local/state/dev"

func ResolveStateDir(cfg *DaemonConfig) (string, error) {
	if cfg != nil && cfg.StateDir != "" {
		expanded, err := ExpandUserPath(cfg.StateDir)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(expanded) {
			return "", errors.New("state_dir must be an absolute path")
		}
		return expanded, nil
	}
	if env := os.Getenv("DEV_STATE_DIR"); env != "" {
		return ExpandUserPath(env)
	}
	return ExpandUserPath(DefaultStateDir)
}

func ResolveGatewayDataDir(cfg *DaemonConfig) (string, error) {
	stateDir, err := ResolveStateDir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "gateway"), nil
}

func ProjectGatewayURL(cfg *ProjectConfig) string {
	if cfg == nil || cfg.Gateway == nil {
		return ""
	}
	raw, _ := cfg.Gateway["url"].(string)
	return strings.TrimSpace(raw)
}

func ResolveGatewayCredentialDir(cfg *DaemonConfig, host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("gateway host is required")
	}
	stateDir, err := ResolveStateDir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "gateway", "agent-credentials", host), nil
}

func ResolveGatewayCredentialDirForURL(cfg *DaemonConfig, gatewayURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(gatewayURL))
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("gateway URL host is required")
	}
	return ResolveGatewayCredentialDir(cfg, host)
}

func ResolveWorktreeDir(cfg *DaemonConfig) (string, error) {
	raw := ""
	if cfg != nil {
		raw = filepath.Clean(strings.TrimSpace(cfg.WorktreeDir))
	}
	if raw == "" || raw == "." {
		stateDir, err := ResolveStateDir(cfg)
		if err != nil {
			return "", err
		}
		return filepath.Join(stateDir, "worktrees"), nil
	}
	expanded, err := ExpandUserPath(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", errors.New("daemon.worktree_dir must be an absolute path")
	}
	return filepath.Clean(expanded), nil
}

// WorktreeLifecycleHookCommands is the resolved set of lifecycle hook commands
// collected from global and per-project daemon hooks.
type WorktreeLifecycleHookCommands struct {
	PreWorktreeAdd      []string
	PostWorktreeAdd     []string
	PreWorktreeCleanup  []string
	PostWorktreeCleanup []string
}

func ResolveDaemonWorktreeLifecycleHooks(daemonCfg *DaemonConfig, projectKeys ...string) WorktreeLifecycleHookCommands {
	resolved := WorktreeLifecycleHookCommands{}
	if daemonCfg == nil {
		return resolved
	}
	appendLifecycleHookCommands(&resolved, daemonCfg.GlobalHooks)
	seenProjectKeys := map[string]struct{}{}
	for _, key := range projectKeys {
		projectKey := strings.TrimSpace(key)
		if projectKey == "" {
			continue
		}
		if _, seen := seenProjectKeys[projectKey]; seen {
			continue
		}
		seenProjectKeys[projectKey] = struct{}{}
		projectHooks, ok := daemonCfg.ProjectHooks[projectKey]
		if !ok {
			continue
		}
		appendLifecycleHookCommands(&resolved, projectHooks)
	}
	return resolved
}

func appendLifecycleHookCommands(dst *WorktreeLifecycleHookCommands, src HooksBlock) {
	if dst == nil {
		return
	}
	if hook := strings.TrimSpace(src.PreWorktreeAdd); hook != "" {
		dst.PreWorktreeAdd = append(dst.PreWorktreeAdd, hook)
	}
	if hook := strings.TrimSpace(src.PostWorktreeAdd); hook != "" {
		dst.PostWorktreeAdd = append(dst.PostWorktreeAdd, hook)
	}
	if hook := strings.TrimSpace(src.PreWorktreeCleanup); hook != "" {
		dst.PreWorktreeCleanup = append(dst.PreWorktreeCleanup, hook)
	}
	if hook := strings.TrimSpace(src.PostWorktreeCleanup); hook != "" {
		dst.PostWorktreeCleanup = append(dst.PostWorktreeCleanup, hook)
	}
}

func ResolveDaemonConfigPath() string {
	if env := os.Getenv("DEV_DAEMON_CONFIG"); env != "" {
		return env
	}
	return DefaultDaemonConfig
}

func validateProjectConfig(cfg *ProjectConfig) error {
	if cfg.Project.Name == "" {
		return errors.New("project.name is required")
	}
	names := make([]string, 0, len(cfg.Processes))
	for name := range cfg.Processes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		proc := cfg.Processes[name]
		ports, err := ParseProcessPorts(proc)
		if err != nil {
			return fmt.Errorf("process.%s: %w", name, err)
		}
		if err := ValidateProcessPorts(proc, ports); err != nil {
			return fmt.Errorf("process.%s: %w", name, err)
		}
	}
	return nil
}

func loadTOMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func mergeMaps(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				mergeMaps(dstMap, srcMap)
				dst[key] = dstMap
				continue
			}
		}
		dst[key] = srcVal
	}
}

// ParseInt extracts an integer from various numeric types commonly encountered
// when parsing configuration values (int, int64, uint, uint64, float32, float64).
// Returns the integer value and true if successful, or 0 and false otherwise.
func ParseInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case uint:
		return int(v), true
	case uint64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
}
