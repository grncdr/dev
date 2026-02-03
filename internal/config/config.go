package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultProjectConfig = ".dev-mode.toml"
	DefaultLocalOverride = ".dev-mode.local.toml"
	DefaultDaemonConfig  = "~/.config/dev-mode/daemon.toml"
)

type ProjectConfig struct {
	Project   ProjectBlock              `toml:"project" json:"project"`
	Gateway   map[string]any            `toml:"gateway" json:"gateway,omitempty"`
	Processes map[string]map[string]any `toml:"process" json:"processes,omitempty"`
	Proxy     ProjectProxyBlock         `toml:"proxy" json:"proxy,omitempty"`
	Hooks     HooksBlock                `toml:"hooks" json:"hooks,omitempty"`
	Commands  CommandsBlock             `toml:"commands" json:"commands,omitempty"`
}

type ProjectBlock struct {
	Name     string `toml:"name" json:"name"`
	MainSlug string `toml:"main_slug" json:"main_slug,omitempty"`
}

type DaemonConfig struct {
	Gateway DaemonGatewayBlock `toml:"gateway" json:"gateway,omitempty"`
	Proxy   DaemonProxyBlock   `toml:"proxy" json:"proxy,omitempty"`
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
	PostCreate string `toml:"post_create" json:"post_create,omitempty"`
	PreCleanup string `toml:"pre_cleanup" json:"pre_cleanup,omitempty"`
	PreStart   string `toml:"pre_start" json:"pre_start,omitempty"`
	PostStart  string `toml:"post_start" json:"post_start,omitempty"`
	PreStop    string `toml:"pre_stop" json:"pre_stop,omitempty"`
	PostStop   string `toml:"post_stop" json:"post_stop,omitempty"`
}

type CommandsBlock struct {
	Wrapper string `toml:"wrapper" json:"wrapper,omitempty"`
}

type ProjectProxyBlock struct {
	// Reserved for future project-level proxy options.
}

type DaemonProxyBlock struct {
	ListenHTTP  string `toml:"listen_http" json:"listen_http,omitempty"`
	ListenHTTPS string `toml:"listen_https" json:"listen_https,omitempty"`
	ApexZone    string `toml:"apex_zone" json:"apex_zone,omitempty"`
	Allow       string `toml:"allow" json:"allow,omitempty"`
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

const DefaultStateDir = "~/.local/state/dev-mode"

func ResolveStateDir() (string, error) {
	if env := os.Getenv("DEV_MODE_STATE_DIR"); env != "" {
		return ExpandUserPath(env)
	}
	return ExpandUserPath(DefaultStateDir)
}

func ResolveGatewayDataDir() (string, error) {
	stateDir, err := ResolveStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "gateway"), nil
}

func ResolveDaemonConfigPath() string {
	if env := os.Getenv("DEV_MODE_DAEMON_CONFIG"); env != "" {
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
