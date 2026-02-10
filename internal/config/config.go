package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultProjectConfig = ".dev.toml"
	DefaultLocalOverride = ".dev.local.toml"
	DefaultDaemonConfig  = "~/.config/dev/daemon.toml"
)

type ProjectConfig struct {
	Project   ProjectBlock              `toml:"project" json:"project"`
	Gateway   map[string]any            `toml:"gateway" json:"gateway,omitempty"`
	Processes map[string]map[string]any `toml:"process" json:"processes,omitempty"`
	Proxy     ProjectProxyBlock         `toml:"proxy" json:"proxy,omitempty"`
	LocalDNS  ProjectLocalDNSBlock      `toml:"local-dns" json:"local_dns,omitempty"`
	Hooks     HooksBlock                `toml:"hooks" json:"hooks,omitempty"`
	Commands  CommandsBlock             `toml:"commands" json:"commands,omitempty"`
}

type ProjectBlock struct {
	Name     string `toml:"name" json:"name"`
	MainSlug string `toml:"main_slug" json:"main_slug,omitempty"`
}

type DaemonConfig struct {
	StateDir     string                `toml:"state_dir" json:"state_dir,omitempty"`
	Gateway      DaemonGatewayBlock    `toml:"gateway" json:"gateway,omitempty"`
	LocalProxy   DaemonLocalProxyBlock `toml:"local-proxy" json:"local_proxy,omitempty"`
	WorktreeDir  string                `toml:"worktree_dir" json:"worktree_dir,omitempty"`
	GlobalHooks  HooksBlock            `toml:"global-hooks" json:"global_hooks,omitempty"`
	ProjectHooks map[string]HooksBlock `toml:"project-hooks" json:"project_hooks,omitempty"`
}

type DaemonGatewayBlock struct {
	Enabled       bool                 `toml:"enabled" json:"enabled,omitempty"`
	Listen        string               `toml:"listen" json:"listen,omitempty"`
	HTTPListen    string               `toml:"http_listen" json:"http_listen,omitempty"`
	DNSZone       string               `toml:"dns_zone" json:"dns_zone,omitempty"`
	Hostname      string               `toml:"hostname" json:"hostname,omitempty"`
	ACMEEmail     string               `toml:"acme_email" json:"acme_email,omitempty"`
	ACMEDir       string               `toml:"acme_directory" json:"acme_directory,omitempty"`
	ACMEStore     string               `toml:"acme_storage" json:"acme_storage,omitempty"`
	ACMEResolvers []string             `toml:"acme_resolvers" json:"acme_resolvers,omitempty"`
	Auth          DaemonGatewayAuth    `toml:"auth" json:"auth,omitempty"`
	Route53       DaemonGatewayRoute53 `toml:"route53" json:"route53,omitempty"`
}

type DaemonGatewayAuth struct {
	Enabled  bool   `toml:"enabled" json:"enabled,omitempty"`
	Username string `toml:"username" json:"username,omitempty"`
	Password string `toml:"password" json:"password,omitempty"`
}

type DaemonGatewayRoute53 struct {
	Enabled      bool   `toml:"enabled" json:"enabled,omitempty"`
	HostedZoneID string `toml:"hosted_zone_id" json:"hosted_zone_id,omitempty"`
	TTL          int64  `toml:"ttl" json:"ttl,omitempty"`
}

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

type CommandsBlock struct {
	Wrapper string `toml:"wrapper" json:"wrapper,omitempty"`
}

type ProjectProxyBlock struct {
	// Reserved for future project-level proxy options.
}

type ProjectLocalDNSBlock struct {
	Overrides map[string]string `toml:"overrides" json:"overrides,omitempty"`
}

type DaemonLocalProxyBlock struct {
	Enabled     *bool  `toml:"enabled" json:"enabled,omitempty"`
	ListenHTTP  string `toml:"listen_http" json:"listen_http,omitempty"`
	ListenHTTPS string `toml:"listen_https" json:"listen_https,omitempty"`
	ApexZone    string `toml:"apex_zone" json:"apex_zone,omitempty"`
	Allow       string `toml:"allow" json:"allow,omitempty"`
}

func (b *DaemonLocalProxyBlock) IsEnabled() bool {
	if b.Enabled == nil {
		return true // default enabled
	}
	return *b.Enabled
}

type LoadInfo struct {
	ConfigPath        string
	LocalOverridePath string
	LocalOverrideUsed bool
	DaemonConfigPath  string
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
