package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeConfigs(t *testing.T) {
	// Test data setup
	existingConfig := &Config{
		Port:          8080,
		GitHubToken:   "existing_github_token",
		CopilotToken:  "existing_copilot_token",
		ExpiresAt:     1234567890,
		RefreshIn:     1500,
		AllowedModels: []string{"gpt-4"},
		DefaultModel:  "gpt-4",
	}
	existingConfig.Timeouts.HTTPClient = 200

	defaultConfig := &Config{
		Port:          7071,
		AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
		DefaultModel:  "gpt-5-mini",
	}
	setDefaultTimeouts(defaultConfig)

	tests := []struct {
		name     string
		mode     ConfigMigrationMode
		existing *Config
		defaults *Config
		validate func(t *testing.T, result *Config)
	}{
		{
			name:     "merge preserves tokens and custom port",
			mode:     ConfigMigrationMerge,
			existing: existingConfig,
			defaults: defaultConfig,
			validate: func(t *testing.T, result *Config) {
				// Should preserve sensitive fields
				if result.GitHubToken != "existing_github_token" {
					t.Errorf("Expected GitHubToken to be preserved, got %s", result.GitHubToken)
				}
				if result.CopilotToken != "existing_copilot_token" {
					t.Errorf("Expected CopilotToken to be preserved, got %s", result.CopilotToken)
				}
				if result.ExpiresAt != 1234567890 {
					t.Errorf("Expected ExpiresAt to be preserved, got %d", result.ExpiresAt)
				}
				if result.RefreshIn != 1500 {
					t.Errorf("Expected RefreshIn to be preserved, got %d", result.RefreshIn)
				}

				// Should preserve custom port (not default 7071)
				if result.Port != 8080 {
					t.Errorf("Expected Port to be preserved, got %d", result.Port)
				}

				// Should update model configuration from defaults
				if result.DefaultModel != "gpt-5-mini" {
					t.Errorf("Expected DefaultModel to be updated to gpt-5-mini, got %s", result.DefaultModel)
				}
				if len(result.AllowedModels) != 3 {
					t.Errorf("Expected 3 allowed models, got %d", len(result.AllowedModels))
				}

				// Should update timeout configuration from defaults
				if result.Timeouts.HTTPClient != 300 {
					t.Errorf("Expected HTTPClient timeout to be updated to default 300, got %d", result.Timeouts.HTTPClient)
				}
			},
		},
		{
			name:     "override replaces everything",
			mode:     ConfigMigrationOverride,
			existing: existingConfig,
			defaults: defaultConfig,
			validate: func(t *testing.T, result *Config) {
				// Should not preserve sensitive fields
				if result.GitHubToken != "" {
					t.Errorf("Expected GitHubToken to be empty after override, got %s", result.GitHubToken)
				}
				if result.CopilotToken != "" {
					t.Errorf("Expected CopilotToken to be empty after override, got %s", result.CopilotToken)
				}
				if result.ExpiresAt != 0 {
					t.Errorf("Expected ExpiresAt to be 0 after override, got %d", result.ExpiresAt)
				}

				// Should use defaults
				if result.Port != 7071 {
					t.Errorf("Expected Port to be default 7071, got %d", result.Port)
				}
				if result.DefaultModel != "gpt-5-mini" {
					t.Errorf("Expected DefaultModel to be gpt-5-mini, got %s", result.DefaultModel)
				}
			},
		},
		{
			name:     "none returns existing unchanged",
			mode:     ConfigMigrationNone,
			existing: existingConfig,
			defaults: defaultConfig,
			validate: func(t *testing.T, result *Config) {
				// Should return existing config unchanged
				if result.GitHubToken != "existing_github_token" {
					t.Errorf("Expected GitHubToken unchanged, got %s", result.GitHubToken)
				}
				if result.Port != 8080 {
					t.Errorf("Expected Port unchanged, got %d", result.Port)
				}
				if result.DefaultModel != "gpt-4" {
					t.Errorf("Expected DefaultModel unchanged, got %s", result.DefaultModel)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeConfigs(tt.existing, tt.defaults, tt.mode)
			tt.validate(t, result)
		})
	}
}

func TestLoadDefaultConfigFromExample(t *testing.T) {
	// Create a temporary config.example.json for testing
	tempDir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	exampleConfig := map[string]interface{}{
		"port":           7071,
		"allowed_models": []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
		"default_model":  "gpt-5-mini",
		"timeouts": map[string]int{
			"http_client": 300,
		},
	}

	file, err := os.Create("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(exampleConfig); err != nil {
		t.Fatal(err)
	}

	// Test loading the config
	config, err := loadDefaultConfigFromExample()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if config.Port != 7071 {
		t.Errorf("Expected port 7071, got %d", config.Port)
	}
	if config.DefaultModel != "gpt-5-mini" {
		t.Errorf("Expected default model gpt-5-mini, got %s", config.DefaultModel)
	}
	if len(config.AllowedModels) != 3 {
		t.Errorf("Expected 3 allowed models, got %d", len(config.AllowedModels))
	}
}

func TestSetDefaultModels_DoesNotOverrideExplicitEmptyAllowedModels(t *testing.T) {
	cfg := &Config{
		AllowedModels: []string{},
		DefaultModel:  "",
	}

	setDefaultModels(cfg)

	if cfg.AllowedModels == nil {
		t.Fatalf("Expected AllowedModels to remain an explicit empty slice, got nil")
	}
	if len(cfg.AllowedModels) != 0 {
		t.Fatalf("Expected AllowedModels to remain empty, got %v", cfg.AllowedModels)
	}
	if cfg.DefaultModel != "gpt-5-mini" {
		t.Fatalf("Expected DefaultModel to be defaulted to gpt-5-mini, got %s", cfg.DefaultModel)
	}
}

func TestMigrateConfigIntegration(t *testing.T) {
	// Create temporary directory for config
	tempDir := t.TempDir()

	// Override config path for testing
	testConfigPath := filepath.Join(tempDir, "config.json")
	originalOverride := configPathOverride
	configPathOverride = testConfigPath
	defer func() { configPathOverride = originalOverride }()

	// Create existing config with tokens
	existingConfig := &Config{
		Port:          8080,
		GitHubToken:   "test_github_token",
		CopilotToken:  "test_copilot_token",
		ExpiresAt:     time.Now().Unix() + 3600,
		RefreshIn:     1500,
		AllowedModels: []string{"gpt-4"},
		DefaultModel:  "gpt-4",
	}
	setDefaultTimeouts(existingConfig)

	// Save existing config
	if err := saveConfig(existingConfig); err != nil {
		t.Fatal(err)
	}

	// Create config.example.json in temp directory
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	exampleConfig := map[string]interface{}{
		"port":           7071,
		"allowed_models": []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
		"default_model":  "gpt-5-mini",
	}

	file, err := os.Create("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(exampleConfig); err != nil {
		t.Fatal(err)
	}

	// Test merge migration
	if err := migrateConfig(ConfigMigrationMerge); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Load and verify migrated config
	migratedConfig, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	// Verify tokens are preserved
	if migratedConfig.GitHubToken != "test_github_token" {
		t.Errorf("GitHubToken not preserved: got %s", migratedConfig.GitHubToken)
	}
	if migratedConfig.CopilotToken != "test_copilot_token" {
		t.Errorf("CopilotToken not preserved: got %s", migratedConfig.CopilotToken)
	}

	// Verify new defaults are applied
	if migratedConfig.DefaultModel != "gpt-5-mini" {
		t.Errorf("DefaultModel not updated: got %s", migratedConfig.DefaultModel)
	}
	if len(migratedConfig.AllowedModels) != 3 {
		t.Errorf("AllowedModels not updated: got %v", migratedConfig.AllowedModels)
	}

	// Verify custom port is preserved
	if migratedConfig.Port != 8080 {
		t.Errorf("Custom port not preserved: got %d", migratedConfig.Port)
	}
}
