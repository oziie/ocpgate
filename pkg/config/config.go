// Package config loads ocpgate's configuration from
// ~/.config/ocpgate/config.yaml, environment variables, and defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// DefaultConfigDir returns ~/.config/ocpgate.
func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ocpgate"), nil
}

// DefaultConfigPath returns ~/.config/ocpgate/config.yaml.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func defaultCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "clusters.json"
	}
	return filepath.Join(home, ".cache", "ocpgate", "clusters.json")
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("registry.branch", "main")
	v.SetDefault("registry.token_env", "OCPGATE_GITLAB_TOKEN")
	v.SetDefault("registry.cache_path", defaultCachePath())
	v.SetDefault("registry.sync_on_start", true)

	v.SetDefault("audit.enabled", true)
	v.SetDefault("audit.writer", "stdout")

	v.SetDefault("tui.default_namespace", "default")
	v.SetDefault("tui.show_environment_badge", true)
}

// Load reads configuration from the given path. If path is empty, the
// default location (~/.config/ocpgate/config.yaml) is used. A missing
// config file is not an error — defaults apply and env vars still layer
// on top, since first-run users won't have written one yet.
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix("OCPGATE")
	v.AutomaticEnv()

	if path == "" {
		def, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		path = def
	}

	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.Registry.CachePath = expandHome(cfg.Registry.CachePath)

	return &cfg, nil
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// GitLabToken resolves the GitLab API token from the environment variable
// named by Registry.TokenEnv. The token is never persisted to config.
func (c *Config) GitLabToken() (string, error) {
	name := c.Registry.TokenEnv
	if name == "" {
		name = "OCPGATE_GITLAB_TOKEN"
	}
	token := os.Getenv(name)
	if token == "" {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return token, nil
}
