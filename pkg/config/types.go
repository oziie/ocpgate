package config

// RegistryConfig configures how the cluster registry is synced and cached.
type RegistryConfig struct {
	GitLabURL   string `mapstructure:"gitlab_url"`
	ProjectID   string `mapstructure:"project_id"`
	Branch      string `mapstructure:"branch"`
	TokenEnv    string `mapstructure:"token_env"`
	CachePath   string `mapstructure:"cache_path"`
	SyncOnStart bool   `mapstructure:"sync_on_start"`
}

// AuditConfig configures audit event emission.
type AuditConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Writer  string `mapstructure:"writer"`
}

// TUIConfig configures default TUI behavior.
type TUIConfig struct {
	DefaultNamespace     string `mapstructure:"default_namespace"`
	ShowEnvironmentBadge bool   `mapstructure:"show_environment_badge"`
}

// Config is the root ocpgate configuration, loaded from
// ~/.config/ocpgate/config.yaml.
type Config struct {
	Registry RegistryConfig `mapstructure:"registry"`
	Audit    AuditConfig    `mapstructure:"audit"`
	TUI      TUIConfig      `mapstructure:"tui"`
}
