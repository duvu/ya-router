package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// Config represents application configuration persisted to disk.
type Config struct {
	Port         int    `json:"port"`
	GitHubToken  string `json:"github_token"`
	CopilotToken string `json:"copilot_token"`
	ExpiresAt    int64  `json:"expires_at"`
	RefreshIn    int64  `json:"refresh_in"`

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

func getConfigPath() (string, error) {
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
		cfg := &Config{Port: 8081}
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
		cfg.Port = 8081
	}

	// Set default timeouts if not specified
	setDefaultTimeouts(&cfg)

	return &cfg, nil
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
