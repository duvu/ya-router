// config.go — application configuration with V0→V1 migration.
package yarouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	configschema "github.com/duvu/ya-router/internal/config"
)

const currentConfigVersion = 1

type SecretReference = configschema.SecretReference
type RoutingConfig = configschema.Routing
type ModelMapEntry = configschema.ModelMapEntry
type VirtualModelConfig = configschema.VirtualModel
type CopilotAuthState = configschema.CopilotAuthState
type CopilotAccount = configschema.CopilotAccount
type CopilotProviderConfig = configschema.CopilotProvider
type CodexAuthState = configschema.CodexAuthState
type CodexAccount = configschema.CodexAccount
type CodexProviderConfig = configschema.CodexProvider
type KiloProviderConfig = configschema.KiloProvider
type ProvidersConfig = configschema.Providers
type TimeoutsConfig = configschema.Timeouts
type LoggingConfig = configschema.Logging
type Config = configschema.Config

// legacyV0Config is used only for reading and migrating pre-V1 config files.
type legacyV0Config struct {
	Port          int      `json:"port"`
	ConfigVersion int      `json:"config_version"`
	GitHubToken   string   `json:"github_token"`
	CopilotToken  string   `json:"copilot_token"`
	ExpiresAt     int64    `json:"expires_at"`
	RefreshIn     int64    `json:"refresh_in"`
	AllowedModels []string `json:"allowed_models"`
	DefaultModel  string   `json:"default_model"`
	EnablePprof   bool     `json:"enable_pprof"`
	Timeouts      struct {
		HTTPClient      int `json:"http_client"`
		ServerRead      int `json:"server_read"`
		ServerWrite     int `json:"server_write"`
		ServerIdle      int `json:"server_idle"`
		ProxyContext    int `json:"proxy_context"`
		CircuitBreaker  int `json:"circuit_breaker"`
		KeepAlive       int `json:"keep_alive"`
		TLSHandshake    int `json:"tls_handshake"`
		DialTimeout     int `json:"dial_timeout"`
		IdleConnTimeout int `json:"idle_conn_timeout"`
	} `json:"timeouts"`
}

// ConfigMigrationMode controls startup migration behaviour.
type ConfigMigrationMode string

const (
	ConfigMigrationNone     ConfigMigrationMode = "none"
	ConfigMigrationMerge    ConfigMigrationMode = "merge"
	ConfigMigrationOverride ConfigMigrationMode = "override"
)

const (
	configDirName         = ".local/share/github-copilot-svcs"
	configDirFallbackName = ".config/ya-router"
	configFileName        = "config.json"
	configPathEnv         = "YA_ROUTER_CONFIG_PATH"
	configDirEnv          = "YA_ROUTER_CONFIG_DIR"
)

// configPathOverride lets tests redirect config I/O to a temp path.
var configPathOverride string

// configWriteMu serializes runtime-owned writes before they enter the
// revisioned state manager.
var configWriteMu sync.Mutex

func getConfigPath() (string, error) {
	if configPathOverride != "" {
		return configPathOverride, nil
	}
	if customPath := strings.TrimSpace(os.Getenv(configPathEnv)); customPath != "" {
		if err := validateConfigPathCandidate(customPath); err != nil {
			return "", fmt.Errorf("invalid %s: %w", configPathEnv, err)
		}
		return customPath, nil
	}
	if customDir := strings.TrimSpace(os.Getenv(configDirEnv)); customDir != "" {
		path := filepath.Join(customDir, configFileName)
		if err := validateConfigPathCandidate(path); err != nil {
			return "", fmt.Errorf("invalid %s: %w", configDirEnv, err)
		}
		return path, nil
	}
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return resolveDefaultConfigPath(usr.HomeDir)
}

func resolveDefaultConfigPath(homeDir string) (string, error) {
	preferred := filepath.Join(homeDir, configDirName, configFileName)
	preferredErr := validateConfigPathCandidate(preferred)
	if preferredErr == nil {
		return preferred, nil
	}
	if !isPermissionError(preferredErr) {
		return "", preferredErr
	}

	fallback := filepath.Join(homeDir, configDirFallbackName, configFileName)
	if fallbackErr := validateConfigPathCandidate(fallback); fallbackErr != nil {
		return "", fmt.Errorf("resolve config path: preferred %q is not usable: %v; fallback %q is not usable: %v", preferred, preferredErr, fallback, fallbackErr)
	}
	return fallback, nil
}

func validateConfigPathCandidate(configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := verifyConfigDirectoryWritable(filepath.Dir(configPath)); err != nil {
		return err
	}
	if fileInfo, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config path: %w", err)
	} else if fileInfo.Mode().IsRegular() {
		file, err := os.Open(configPath)
		if err != nil {
			return fmt.Errorf("open config path: %w", err)
		}
		_ = file.Close()
	}
	return nil
}

func verifyConfigDirectoryWritable(dir string) error {
	marker, err := os.CreateTemp(dir, ".ya-router-config-write-")
	if err != nil {
		return fmt.Errorf("test config directory writeability: %w", err)
	}
	if err := marker.Close(); err != nil {
		_ = os.Remove(marker.Name())
		return err
	}
	if err := os.Remove(marker.Name()); err != nil {
		return err
	}
	return nil
}

func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) || os.IsPermission(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "permission denied")
}

// loadConfig returns the daemon-owned desired configuration when managed state
// is active, otherwise it reads the compatibility config path directly.
func loadConfig() (*Config, error) {
	if manager := currentConfigState(); manager != nil {
		return configschema.Clone(manager.Snapshot().Desired), nil
	}
	path, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	return loadConfigFromPath(path)
}

// loadConfigFromPath reads config from an explicit path and auto-migrates V0
// content in memory. The caller owns any persistence decision.
func loadConfigFromPath(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("open config: %w (ensure YA_ROUTER_CONFIG_DIR or YA_ROUTER_CONFIG_PATH points to a writable location)", err)
		}
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var probe struct {
		Version int `json:"config_version"`
		Routing struct {
			ExposeInternalModels *bool `json:"expose_internal_models"`
		} `json:"routing"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Version >= 1 {
		var cfg Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		applyConfigDefaults(&cfg)
		if probe.Routing.ExposeInternalModels == nil {
			// An existing config predating issue #87 has no explicit setting;
			// preserve its current /v1/models discovery behavior rather than
			// silently hiding models an operator never opted to hide.
			cfg.Routing.ExposeInternalModels = true
		}
		return &cfg, nil
	}
	var legacy legacyV0Config
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, err
	}
	return migrateV0ToV1(&legacy), nil
}
