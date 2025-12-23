package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// ConfigMigrationMode specifies how to handle config migration
type ConfigMigrationMode string

const (
	ConfigMigrationNone     ConfigMigrationMode = "none"
	ConfigMigrationMerge    ConfigMigrationMode = "merge"
	ConfigMigrationOverride ConfigMigrationMode = "override"
)

// Config represents application configuration persisted to disk.
type Config struct {
	Port         int    `json:"port"`
	GitHubToken  string `json:"github_token"`
	CopilotToken string `json:"copilot_token"`
	ExpiresAt    int64  `json:"expires_at"`
	RefreshIn    int64  `json:"refresh_in"`

	// Model configuration
	AllowedModels []string `json:"allowed_models"`
	DefaultModel  string   `json:"default_model"`

	// Timeout configurations (in seconds)
	Timeouts struct {
		HTTPClient      int `json:"http_client"`       // Default: 300s for streaming responses
		ServerRead      int `json:"server_read"`       // Default: 30s for request reading
		ServerWrite     int `json:"server_write"`      // Default: 300s for streaming responses
		ServerIdle      int `json:"server_idle"`       // Default: 120s for idle connections
		ProxyContext    int `json:"proxy_context"`     // Default: 300s for proxy request context
		CircuitBreaker  int `json:"circuit_breaker"`   // Default: 30s for circuit breaker recovery
		KeepAlive       int `json:"keep_alive"`        // Default: 30s for connection keep-alive
		TLSHandshake    int `json:"tls_handshake"`     // Default: 10s for TLS handshake
		DialTimeout     int `json:"dial_timeout"`      // Default: 10s for connection dialing
		IdleConnTimeout int `json:"idle_conn_timeout"` // Default: 90s for idle connection timeout
	} `json:"timeouts"`
}

const (
	configDirName  = ".local/share/github-copilot-svcs"
	configFileName = "config.json"
)

// configPathOverride allows tests to override the config path
var configPathOverride string

func getConfigPath() (string, error) {
	if configPathOverride != "" {
		return configPathOverride, nil
	}

	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(usr.HomeDir, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil { // ensure private dir
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

func loadConfig() (*Config, error) {
	path, err := getConfigPath()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		// Return default config if file doesn't exist
		cfg := &Config{Port: 7071}
		setDefaultModels(cfg)
		setDefaultTimeouts(cfg)
		return cfg, nil
	}
	defer file.Close()

	var cfg Config
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return nil, err
	}

	// Set default port if not specified
	if cfg.Port == 0 {
		cfg.Port = 7071
	}

	// Set default models if not specified
	setDefaultModels(&cfg)

	// Set default timeouts if not specified
	setDefaultTimeouts(&cfg)

	return &cfg, nil
}

// setDefaultModels sets default model configuration if not specified
func setDefaultModels(cfg *Config) {
	// IMPORTANT: distinguish between "not provided" (nil) and "provided empty" ([]).
	// - nil  => apply safe defaults
	// - []   => user explicitly wants to allow all discovered models
	if cfg.AllowedModels == nil {
		cfg.AllowedModels = []string{"gpt-4", "gpt-4.1", "gpt-5-mini"}
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "gpt-5-mini"
	}
}

// setDefaultTimeouts sets default timeout values if they are zero
func setDefaultTimeouts(cfg *Config) {
	if cfg.Timeouts.HTTPClient == 0 {
		cfg.Timeouts.HTTPClient = 300
	}
	if cfg.Timeouts.ServerRead == 0 {
		cfg.Timeouts.ServerRead = 30
	}
	if cfg.Timeouts.ServerWrite == 0 {
		cfg.Timeouts.ServerWrite = 300
	}
	if cfg.Timeouts.ServerIdle == 0 {
		cfg.Timeouts.ServerIdle = 120
	}
	if cfg.Timeouts.ProxyContext == 0 {
		cfg.Timeouts.ProxyContext = 300
	}
	if cfg.Timeouts.CircuitBreaker == 0 {
		cfg.Timeouts.CircuitBreaker = 30
	}
	if cfg.Timeouts.KeepAlive == 0 {
		cfg.Timeouts.KeepAlive = 30
	}
	if cfg.Timeouts.TLSHandshake == 0 {
		cfg.Timeouts.TLSHandshake = 10
	}
	if cfg.Timeouts.DialTimeout == 0 {
		cfg.Timeouts.DialTimeout = 10
	}
	if cfg.Timeouts.IdleConnTimeout == 0 {
		cfg.Timeouts.IdleConnTimeout = 90
	}
}

func saveConfig(cfg *Config) error {
	path, err := getConfigPath()
	if err != nil {
		return err
	}
	// Write atomically with correct permissions
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(cfg); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp config: %w", err)
	}
	return nil
}

// loadDefaultConfigFromExample loads default configuration from config.example.json
func loadDefaultConfigFromExample() (*Config, error) {
	examplePath := "config.example.json"

	// Try to read from config.example.json if it exists
	if _, err := os.Stat(examplePath); err == nil {
		file, err := os.Open(examplePath)
		if err != nil {
			return nil, fmt.Errorf("failed to open config.example.json: %w", err)
		}
		defer file.Close()

		var cfg Config
		if err := json.NewDecoder(file).Decode(&cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config.example.json: %w", err)
		}

		// Apply default values for any missing fields
		setDefaultModels(&cfg)
		setDefaultTimeouts(&cfg)
		if cfg.Port == 0 {
			cfg.Port = 7071
		}

		return &cfg, nil
	}

	// Fallback to hardcoded defaults
	cfg := &Config{Port: 7071}
	setDefaultModels(cfg)
	setDefaultTimeouts(cfg)
	return cfg, nil
}

// mergeConfigs merges default configuration into existing config while preserving sensitive fields
func mergeConfigs(existing *Config, defaults *Config, mode ConfigMigrationMode) *Config {
	switch mode {
	case ConfigMigrationOverride:
		// Complete override - use defaults but preserve nothing
		return defaults

	case ConfigMigrationMerge:
		// Merge mode - preserve sensitive fields, update others
		merged := *defaults // Start with defaults

		// Preserve sensitive fields from existing config
		if existing.GitHubToken != "" {
			merged.GitHubToken = existing.GitHubToken
		}
		if existing.CopilotToken != "" {
			merged.CopilotToken = existing.CopilotToken
		}
		if existing.ExpiresAt > 0 {
			merged.ExpiresAt = existing.ExpiresAt
		}
		if existing.RefreshIn > 0 {
			merged.RefreshIn = existing.RefreshIn
		}

		// Preserve port if it was customized (not default 7071)
		if existing.Port != 0 && existing.Port != 7071 {
			merged.Port = existing.Port
		}

		return &merged

	case ConfigMigrationNone:
		fallthrough
	default:
		// No migration - return existing config as-is
		return existing
	}
}

// migrateConfig performs config migration based on the specified mode
func migrateConfig(mode ConfigMigrationMode) error {
	if mode == ConfigMigrationNone {
		return nil
	}

	// Load existing configuration
	existingConfig, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}

	// Load default configuration from example
	defaultConfig, err := loadDefaultConfigFromExample()
	if err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}

	// Check if migration is actually needed
	path, err := getConfigPath()
	if err != nil {
		return err
	}

	// If config file doesn't exist, no migration needed
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	// Perform migration
	mergedConfig := mergeConfigs(existingConfig, defaultConfig, mode)

	// Save the merged configuration
	if err := saveConfig(mergedConfig); err != nil {
		return fmt.Errorf("failed to save migrated config: %w", err)
	}

	fmt.Printf("Config migration completed (mode: %s)\n", mode)
	return nil
}
