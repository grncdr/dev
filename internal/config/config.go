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
	DefaultUserConfig    = "~/.config/dev-mode/config.toml"
)

type ProjectConfig struct {
	Project   ProjectBlock              `toml:"project"`
	Gateway   map[string]any            `toml:"gateway"`
	Services  map[string]map[string]any `toml:"services"`
	Processes map[string]map[string]any `toml:"processes"`
	Proxy     map[string]any            `toml:"proxy"`
}

type ProjectBlock struct {
	Name     string `toml:"name"`
	ApexZone string `toml:"apex_zone"`
}

type UserConfig struct {
	Worktrees map[string]WorktreeTrust `toml:"worktrees"`
	Gateway   map[string]any           `toml:"gateway"`
}

type WorktreeTrust struct {
	Project string `toml:"project"`
	Trusted bool   `toml:"trusted"`
}

type LoadInfo struct {
	ConfigPath        string
	LocalOverridePath string
	LocalOverrideUsed bool
	UserConfigPath    string
	UserConfigFound   bool
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

func LoadUserConfig(userConfigPath string) (*UserConfig, *LoadInfo, error) {
	info := &LoadInfo{UserConfigPath: userConfigPath}

	if _, err := os.Stat(userConfigPath); errors.Is(err, os.ErrNotExist) {
		return &UserConfig{}, info, nil
	}

	data, err := os.ReadFile(userConfigPath)
	if err != nil {
		return nil, info, fmt.Errorf("read user config: %w", err)
	}

	var cfg UserConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, info, fmt.Errorf("decode user config: %w", err)
	}

	info.UserConfigFound = true
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

func validateProjectConfig(cfg *ProjectConfig) error {
	if cfg.Project.Name == "" {
		return errors.New("project.name is required")
	}
	if cfg.Project.ApexZone == "" {
		return errors.New("project.apex_zone is required")
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
