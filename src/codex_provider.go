// codex_provider.go — OpenAI Codex backend provider implementation.
// Auth tokens are persisted in the project config folder alongside
// Copilot tokens.  The token value itself is never logged.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const codexAPIBase = "https://api.openai.com/v1"

// CodexProvider implements Provider for the OpenAI Codex / API backend.
type CodexProvider struct {
	cfg   *Config
	mu    sync.Mutex
	cb    *CircuitBreaker
	cache *ModelCache
}

// isDeviceCodeMode returns true for both the canonical "device_code" name
// and the legacy alias "chatgpt_device_auth".
func isDeviceCodeMode(mode string) bool {
	return mode == "device_code" || mode == "chatgpt_device_auth"
}

// NewCodexProvider constructs a CodexProvider from cfg.
func NewCodexProvider(cfg *Config) *CodexProvider {
	return &CodexProvider{
		cfg: cfg,
		cb: &CircuitBreaker{
			state:   CircuitClosed,
			timeout: time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second,
		},
		cache: NewModelCache(defaultModelCacheTTL),
	}
}

func (p *CodexProvider) ID() ProviderID { return ProviderCodex }
func (p *CodexProvider) Name() string   { return "OpenAI Codex" }
func (p *CodexProvider) Capabilities() []Capability {
	return []Capability{CapabilityChat, CapabilityEmbeddings}
}

func (p *CodexProvider) authState() *CodexAuthState {
	return &p.cfg.Providers.Codex.Auth
}

func (p *CodexProvider) save() error {
	return saveConfig(p.cfg)
}

// currentToken returns the current access token under lock.
func (p *CodexProvider) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authState().AccessToken
}

// EnsureAuthenticated ensures a valid Codex credential is available.
// For api_key mode it reads from the environment variable.
// For device_code mode it mirrors Copilot's pattern: check expiry,
// attempt refresh, fall back to re-authentication error.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	auth := p.authState()

	if auth.Mode == "api_key" {
		envVar := auth.APIKeyEnv
		if envVar == "" {
			envVar = "OPENAI_API_KEY"
		}
		key := os.Getenv(envVar)
		if key == "" {
			return fmt.Errorf("env var %q is not set", envVar)
		}
		auth.AccessToken = key
		return nil
	}

	// device_code mode — token stored in config.
	if auth.AccessToken == "" {
		log.Printf("[codex] no access token available")
		return fmt.Errorf("no Codex token — run 'auth codex' first")
	}

	// Check expiry and refresh if needed.
	now := time.Now().Unix()
	if auth.ExpiresAt > 0 {
		remaining := auth.ExpiresAt - now
		threshold := int64(300) // 5 min safety margin
		if remaining <= threshold {
			log.Printf("[codex] token expiring in %ds (threshold=%ds), refreshing...", remaining, threshold)
			if err := codexRefreshToken(auth, p.save); err != nil {
				log.Printf("[codex] refresh failed: %v — re-authenticating", err)
				return codexAuthenticate(auth, p.save)
			}
			log.Printf("[codex] token refreshed, new expiry in %ds", auth.ExpiresAt-time.Now().Unix())
		} else {
			log.Printf("[codex] token valid, expires in %dm%ds", remaining/60, remaining%60)
		}
	}
	return nil
}

// codexKnownModels is the canonical list of models supported by the Codex
// backend, sourced from the official Codex CLI bundled models.json.
// All entries have supported_in_api=true in the upstream source.
var codexKnownModels = []Model{
	{ID: "gpt-5.3-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.4", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.2-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-max", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.2", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-oss-120b", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-oss-20b", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-mini", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5-codex-mini", Object: "model", OwnedBy: "openai"},
}

// ListModels returns the OpenAI model list filtered by AllowedModels.
func (p *CodexProvider) ListModels(ctx context.Context) (*ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return p.cache.GetOrFetch(func() (*ModelList, error) {
		return p.fetchModels(ctx)
	})
}

// fetchModels fetches models from the upstream API or returns the known list.
func (p *CodexProvider) fetchModels(ctx context.Context) (*ModelList, error) {
	// device_code JWT lacks api.model.read scope → use known models list.
	if isDeviceCodeMode(p.authState().Mode) {
		return p.knownModelList(), nil
	}

	// api_key mode — real API key can list models.
	req, err := http.NewRequestWithContext(ctx, "GET", codexAPIBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	token := p.currentToken()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "github-copilot-svcs/1.0")

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("codex /models returned %d — using known models", resp.StatusCode)
		return p.knownModelList(), nil
	}
	var ml ModelList
	if err := json.NewDecoder(resp.Body).Decode(&ml); err != nil {
		return nil, err
	}
	if len(p.cfg.Providers.Codex.AllowedModels) > 0 {
		return filterAllowedModels(&ml, p.cfg.Providers.Codex.AllowedModels), nil
	}
	return &ml, nil
}

// knownModelList returns the canonical Codex model list, merging the hardcoded
// known models with any additional entries from routing.model_map.
func (p *CodexProvider) knownModelList() *ModelList {
	now := time.Now().Unix()
	seen := make(map[string]bool, len(codexKnownModels))
	models := make([]Model, 0, len(codexKnownModels))
	for _, m := range codexKnownModels {
		m.Created = now
		seen[m.ID] = true
		models = append(models, m)
	}
	// Also include any routing.model_map entries targeting codex.
	for modelID, entry := range p.cfg.Routing.ModelMap {
		if ProviderID(entry.Provider) == ProviderCodex && !seen[modelID] {
			models = append(models, Model{
				ID:      modelID,
				Object:  "model",
				Created: now,
				OwnedBy: "openai",
			})
		}
	}
	ml := &ModelList{Object: "list", Data: models}
	if len(p.cfg.Providers.Codex.AllowedModels) > 0 {
		return filterAllowedModels(ml, p.cfg.Providers.Codex.AllowedModels)
	}
	return ml
}

// ProxyRequest proxies chat or embeddings requests to the OpenAI API.
func (p *CodexProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	cap Capability,
) error {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		log.Printf("[codex] auth failed: %v", err)
		return fmt.Errorf("codex auth: %w", err)
	}
	if !p.cb.canExecute() {
		log.Printf("[codex] circuit breaker OPEN — rejecting request")
		return fmt.Errorf("codex circuit breaker is open")
	}

	targetPath := "/chat/completions"
	if cap == CapabilityEmbeddings {
		targetPath = "/embeddings"
	}

	upstreamURL := codexAPIBase + targetPath
	log.Printf("[codex] proxying %s → %s (body %d bytes, mode=%s)",
		cap, upstreamURL, len(body), p.authState().Mode)

	token := p.currentToken()
	req, err := http.NewRequestWithContext(ctx, r.Method,
		upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "github-copilot-svcs/1.0")

	resp, err := makeRequestWithRetry(sharedHTTPClient, req, body)
	if err != nil {
		log.Printf("[codex] upstream error: %v", err)
		p.cb.onFailure()
		return err
	}
	defer resp.Body.Close()

	log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// Log client error response body for debugging (capped at 512 bytes).
		peekBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("[codex] upstream %d response: %s", resp.StatusCode, string(peekBody))
		// Re-create body for streaming to client.
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peekBody), resp.Body))
	}

	if resp.StatusCode < 500 {
		p.cb.onSuccess()
	} else {
		log.Printf("[codex] upstream 5xx error — circuit breaker failure")
		p.cb.onFailure()
	}
	return streamResponse(w, resp)
}

// Health returns the provider's authentication state.
func (p *CodexProvider) Health(_ context.Context) ProviderHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := p.authState()
	authenticated := auth.AccessToken != ""
	if auth.Mode != "api_key" && auth.ExpiresAt > 0 {
		authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
	}
	hasRefresh := auth.RefreshToken != "" || auth.Mode == "api_key"
	return ProviderHealth{Authenticated: authenticated, CanRefresh: hasRefresh}
}
