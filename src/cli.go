// cli.go — command-line command handlers.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"
)

func printUsage() {
	fmt.Printf("GitHub Copilot SVCS Proxy\n\n")
	fmt.Printf("Usage: %s [command] [options]\n\n", os.Args[0])
	fmt.Printf("Commands:\n")
	fmt.Printf("  run|start             Start the proxy server\n")
	fmt.Printf("    --config-migrate    Config migration mode: merge (default), none, override\n")
	fmt.Printf("  auth [copilot|codex]  Authenticate a provider (default: copilot)\n")
	fmt.Printf("    --mode              Auth mode: device_code (default)\n")
	fmt.Printf("    --api-key <key>     Use OpenAI Platform API key (codex only)\n")
	fmt.Printf("    --token <token>     Manually set access token (codex only, fallback)\n")
	fmt.Printf("  status                Show authentication status for all providers\n")
	fmt.Printf("  config                Show current configuration\n")
	fmt.Printf("  models [--provider P] List models (all providers or a specific one)\n")
	fmt.Printf("  refresh [--provider P] Force token refresh\n")
	fmt.Printf("  migrate-config        Migrate configuration file\n")
	fmt.Printf("    --mode              Migration mode: merge (default), override\n")
	fmt.Printf("  version               Show version information\n")
	fmt.Printf("  help                  Show this help\n\n")
	flag.PrintDefaults()
}

// handleAuthCopilot runs the GitHub device-flow for Copilot.
// mode is ignored for Copilot (always device_code) but accepted for CLI consistency.
func handleAuthCopilot(mode string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)
	cfg.Providers.Copilot.Enabled = true
	cfg.Providers.Copilot.Auth.Mode = "device_code"
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to persist Copilot config: %w", err)
	}
	fmt.Println("Starting GitHub Copilot authentication (mode: device_code)...")
	auth := &cfg.Providers.Copilot.Auth
	if err := copilotAuthenticate(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	fmt.Println("Authentication successful!")
	return nil
}

// handleAuthCodex runs the OpenAI OAuth device-code flow for Codex and persists
// ChatGPT-backed credentials to the official Codex auth store.
func handleAuthCodex(_ string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.Auth.Mode = "chatgpt"

	fmt.Println("Starting OpenAI Codex authentication (mode: chatgpt device_code)...")

	auth := &CodexAuthState{Mode: "chatgpt"}
	if err := codexAuthenticate(auth, func() error { return nil }); err != nil {
		return fmt.Errorf("Codex authentication failed: %w", err)
	}

	if err := persistToOfficialStore(auth); err != nil {
		fmt.Printf("Warning: could not write to official Codex store: %v\n", err)
	} else {
		p, _ := officialAuthJSONPath()
		fmt.Printf("Tokens also saved to %s\n", p)
	}
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to persist Codex auth mode: %w", err)
	}

	fmt.Println("Codex credentials validated successfully!")
	return nil
}

// handleAuthCodexAPIKey stores an OpenAI Platform API key for Codex.
func handleAuthCodexAPIKey(apiKey string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.Auth.Mode = "api_key"
	cfg.Providers.Codex.Auth.APIKey = apiKey
	// Clear any ChatGPT tokens.
	cfg.Providers.Codex.Auth.AccessToken = ""
	cfg.Providers.Codex.Auth.RefreshToken = ""
	cfg.Providers.Codex.Auth.ExpiresAt = 0
	cfg.Providers.Codex.Auth.AccountID = ""

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex API key saved (mode: api_key → api.openai.com/v1).")
	return nil
}

// handleAuthCodexManualToken sets a manually-provided access token.
// Useful as a fallback for environments where device-code flow fails.
func handleAuthCodexManualToken(token string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.Auth.Mode = "chatgpt"
	auth := &CodexAuthState{Mode: "chatgpt", AccessToken: token, ExpiresAt: time.Now().Unix() + 86400}
	if err := persistToOfficialStore(auth); err != nil {
		return fmt.Errorf("failed to persist official Codex auth store: %w", err)
	}
	clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)

	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Println("Codex access token saved successfully!")
	fmt.Println("Note: token expiry is treated as 24h for this manual fallback. Run 'refresh --provider codex' later if needed.")
	return nil
}

// handleStatus prints authentication status for each configured provider.
func handleStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	path, _ := getConfigPath()
	fmt.Printf("Configuration: %s\n", path)
	fmt.Printf("Port: %d\n\n", cfg.Port)

	if cfg.Providers.Copilot.Enabled {
		printCopilotStatus(&cfg.Providers.Copilot.Auth)
	}
	if cfg.Providers.Codex.Enabled {
		printCodexStatus(cfg)
	}
	return nil
}

func printCopilotStatus(auth *CopilotAuthState) {
	fmt.Println("Provider: GitHub Copilot")
	now := time.Now().Unix()
	if auth.CopilotToken != "" {
		remaining := auth.ExpiresAt - now
		if remaining > 0 {
			fmt.Printf("  Auth: ✓ Authenticated (expires in %dm %ds)\n", remaining/60, remaining%60)
			threshold := int64(300)
			if auth.RefreshIn > 0 {
				if t := auth.RefreshIn / 5; t > threshold {
					threshold = t
				}
			}
			if remaining <= threshold {
				fmt.Printf("  Status: ⚠  Refresh imminent\n")
			} else {
				fmt.Printf("  Status: ✅ Token healthy\n")
			}
		} else {
			fmt.Printf("  Auth: ⚠  Token EXPIRED (%d s ago)\n", -remaining)
		}
		fmt.Printf("  Has GitHub token: %t\n", auth.GitHubToken != "")
	} else {
		fmt.Printf("  Auth: ✗ Not authenticated — run '%s auth copilot'\n", os.Args[0])
	}
	fmt.Println()
}

func printCodexStatus(cfg *Config) {
	fmt.Println("Provider: OpenAI Codex")
	auth := &cfg.Providers.Codex.Auth
	now := time.Now().Unix()

	if isAPIKeyMode(auth.Mode) {
		key, source, err := resolveCodexAPIKey(auth)
		if err != nil {
			fmt.Printf("  Auth: ⚠  Credential lookup failed: %v\n", err)
		} else if key != "" {
			fmt.Printf("  Auth: ✓ API key configured\n")
			fmt.Printf("  Credential source: %s\n", source)
			fmt.Printf("  Backend: api.openai.com/v1\n")
		} else {
			fmt.Printf("  Auth: ✗ No API key — run '%s auth codex --api-key <key>'\n", os.Args[0])
		}
		fmt.Printf("  Mode: %s\n", auth.Mode)
		fmt.Println()
		return
	}

	resolved, err := resolveCodexChatGPTAuth(auth)
	if err != nil {
		fmt.Printf("  Auth: ⚠  Credential lookup failed: %v\n", err)
		fmt.Printf("  Mode: %s\n", auth.Mode)
		fmt.Printf("  Backend: %s\n", defaultChatGPTBaseURL)
		fmt.Println()
		return
	}

	if resolved != nil {
		if resolved.ExpiresAt > 0 {
			remaining := resolved.ExpiresAt - now
			if remaining > 0 {
				fmt.Printf("  Auth: ✓ Authenticated (expires in %dm %ds)\n",
					remaining/60, remaining%60)
				if remaining <= 300 {
					fmt.Printf("  Status: ⚠  Refresh imminent\n")
				} else {
					fmt.Printf("  Status: ✅ Token healthy\n")
				}
			} else {
				fmt.Printf("  Auth: ⚠  Token EXPIRED (%d s ago)\n", -remaining)
			}
		} else {
			fmt.Printf("  Auth: ✓ Token available (no expiry info)\n")
		}
		fmt.Printf("  Refreshable: %t\n", resolved.RefreshToken != "")
		fmt.Printf("  Has account metadata: %t\n", resolved.AccountID != "")
		fmt.Printf("  Credential source: %s\n", resolved.Source)
	} else {
		fmt.Printf("  Auth: ✗ Not authenticated — run '%s auth codex'\n", os.Args[0])
	}
	fmt.Printf("  Mode: %s\n", auth.Mode)
	fmt.Printf("  Backend: %s\n", defaultChatGPTBaseURL)
	fmt.Println()
}

// handleConfig prints the current configuration.
func handleConfig() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	path, _ := getConfigPath()
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Printf("Config version: %d\n", cfg.ConfigVersion)
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("Default model: %s\n", cfg.Routing.DefaultModel)
	fmt.Printf("Default provider: %s\n", cfg.Routing.DefaultProvider)
	fmt.Printf("Copilot enabled: %t\n", cfg.Providers.Copilot.Enabled)
	fmt.Printf("Codex enabled: %t\n", cfg.Providers.Codex.Enabled)
	fmt.Printf("Copilot allowed models: %v\n", cfg.Providers.Copilot.AllowedModels)
	return nil
}

func getCurrentTime() int64 {
	return time.Now().Unix()
}

// handleRunWithMigration migrates config then starts the proxy server.
func handleRunWithMigration(migrationMode ConfigMigrationMode) error {
	if migrationMode != ConfigMigrationNone {
		fmt.Printf("Running config migration (mode: %s)...\n", migrationMode)
		if err := migrateConfig(migrationMode); err != nil {
			return fmt.Errorf("config migration failed: %w", err)
		}
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	// Ensure codex known models are in model_map (persisted to config).
	if cfg.Providers.Codex.Enabled {
		if ensureCodexModelMap(cfg) {
			if err := saveConfig(cfg); err != nil {
				fmt.Printf("Warning: failed to persist codex model_map: %v\n", err)
			}
		}
	}

	// Build provider registry.
	registry := NewProviderRegistry()
	if cfg.Providers.Copilot.Enabled {
		registry.Register(NewCopilotProvider(cfg))
	}
	if cfg.Providers.Codex.Enabled {
		registry.Register(NewCodexProvider(cfg))
	}

	// Eagerly authenticate; non-fatal per provider.
	ctx := context.Background()
	for _, p := range registry.All() {
		if err := p.EnsureAuthenticated(ctx); err != nil {
			fmt.Printf("Warning: provider %s auth failed: %v\n", p.ID(), err)
		}
	}

	router := NewModelRouter(registry, cfg.Routing)

	setupLogging()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", modelsHandler(registry, cfg))
	mux.HandleFunc("/v1/models/", modelsHandler(registry, cfg))
	mux.HandleFunc("/v1/embeddings", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/embeddings/", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/chat/completions", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/v1/chat/completions/", proxyHandler(registry, router, cfg))
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/health/", healthHandler)
	if cfg.EnablePprof {
		mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
		mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)
	}

	port := cfg.Port
	if port == 0 {
		port = 8081
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  time.Duration(cfg.Timeouts.ServerRead) * time.Second,
		WriteTimeout: time.Duration(cfg.Timeouts.ServerWrite) * time.Second,
		IdleTimeout:  time.Duration(cfg.Timeouts.ServerIdle) * time.Second,
	}
	setupGracefulShutdown(server)

	fmt.Printf("Starting proxy on :%d\n", port)
	fmt.Printf("  /v1/models              → aggregated from all providers\n")
	fmt.Printf("  /v1/chat/completions    → routed per model\n")
	fmt.Printf("  /v1/embeddings          → routed per model\n")
	if cfg.EnablePprof {
		fmt.Printf("  /debug/pprof/           → enabled\n")
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// handleModels lists models from the given provider (or all providers).
func handleModels(providerFilter string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	registry := NewProviderRegistry()
	if cfg.Providers.Copilot.Enabled {
		registry.Register(NewCopilotProvider(cfg))
	}
	if cfg.Providers.Codex.Enabled {
		registry.Register(NewCodexProvider(cfg))
	}

	ctx := context.Background()
	for _, p := range registry.All() {
		if providerFilter != "" && string(p.ID()) != providerFilter {
			continue
		}
		ml, err := p.ListModels(ctx)
		if err != nil {
			fmt.Printf("[%s] error: %v\n", p.Name(), err)
			continue
		}
		fmt.Printf("[%s] %d model(s):\n", p.Name(), len(ml.Data))
		for _, m := range ml.Data {
			fmt.Printf("  - %s (%s)\n", m.ID, m.OwnedBy)
		}
	}
	return nil
}

// handleRefresh forces a token refresh for Copilot (and/or Codex when applicable).
func handleRefresh(providerFilter string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	initializeTimeouts(cfg)

	if providerFilter == "" || providerFilter == string(ProviderCopilot) {
		if err := refreshCopilot(cfg); err != nil {
			return err
		}
	}
	if (providerFilter == "" || providerFilter == string(ProviderCodex)) && cfg.Providers.Codex.Enabled {
		if err := refreshCodex(cfg); err != nil {
			return err
		}
	}
	return nil
}

func refreshCopilot(cfg *Config) error {
	auth := &cfg.Providers.Copilot.Auth
	if auth.CopilotToken == "" {
		return fmt.Errorf("no Copilot token - run 'auth copilot' first")
	}
	fmt.Println("Refreshing Copilot token...")
	if err := copilotRefreshToken(auth, func() error { return saveConfig(cfg) }); err != nil {
		return fmt.Errorf("Copilot refresh failed: %w", err)
	}
	remaining := auth.ExpiresAt - time.Now().Unix()
	fmt.Printf("✅ Copilot token refreshed (expires in %dm %ds)\n", remaining/60, remaining%60)
	return nil
}

func refreshCodex(cfg *Config) error {
	auth := &cfg.Providers.Codex.Auth
	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		if err != nil {
			return err
		}
		if key == "" {
			return fmt.Errorf("no Codex API key - run 'auth codex --api-key' first")
		}
		fmt.Println("Codex API key mode does not use token refresh.")
		return nil
	}

	working := *auth
	resolved, err := resolveCodexChatGPTAuth(auth)
	if err != nil {
		return err
	}
	if resolved != nil {
		applyResolvedCodexChatGPTAuth(&working, resolved)
	}
	if working.AccessToken == "" {
		return fmt.Errorf("no Codex token — run 'auth codex' first")
	}
	if working.RefreshToken == "" {
		fmt.Println("Codex: no refresh token available, re-authenticating...")
		return handleAuthCodex("chatgpt")
	}
	fmt.Println("Refreshing Codex token...")
	if err := codexRefreshToken(&working, func() error { return nil }); err != nil {
		fmt.Printf("Refresh failed: %v — re-authenticating...\n", err)
		return handleAuthCodex("chatgpt")
	}
	if err := persistToOfficialStore(&working); err != nil {
		return fmt.Errorf("persist refreshed Codex credentials: %w", err)
	}
	clearPersistedChatGPTSecrets(auth)
	if err := saveConfig(cfg); err != nil {
		return fmt.Errorf("persist Codex config: %w", err)
	}
	remaining := working.ExpiresAt - time.Now().Unix()
	fmt.Printf("✅ Codex token refreshed (expires in %dm %ds)\n", remaining/60, remaining%60)
	return nil
}
