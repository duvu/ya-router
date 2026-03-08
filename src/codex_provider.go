// codex_provider.go — OpenAI Codex backend provider implementation.
//
// Two transport modes:
//   - ChatGPT mode (device_code / chatgpt): sends to chatgpt.com/backend-api/
//     with chatgpt-account-id header.  Reads credentials from the official
//     Codex auth store (~/.codex/auth.json).
//   - API key mode: sends to api.openai.com/v1/ with Bearer API key.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// Default upstream URLs per transport mode.
	defaultChatGPTBaseURL  = "https://chatgpt.com/backend-api/"
	defaultPlatformBaseURL = "https://api.openai.com/v1"
)

// CodexProvider implements Provider for the OpenAI Codex / API backend.
type CodexProvider struct {
	cfg   *Config
	mu    sync.Mutex
	cb    *CircuitBreaker
	cache *ModelCache
}

// chatgptBaseURL returns the ChatGPT backend URL, honouring the config
// override and ensuring a trailing slash.
func (p *CodexProvider) chatgptBaseURL() string {
	if u := p.cfg.Providers.Codex.ChatGPTBaseURL; u != "" {
		if !strings.HasSuffix(u, "/") {
			return u + "/"
		}
		return u
	}
	return defaultChatGPTBaseURL
}

// setChatGPTHeaders sets headers for ChatGPT-backend requests.
func setChatGPTHeaders(req *http.Request, token, accountID string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
}

// setPlatformHeaders sets headers for OpenAI Platform API requests.
func setPlatformHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)
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

// authCredentials returns the current token, account ID, and whether this
// is ChatGPT mode, all under lock.
func (p *CodexProvider) authCredentials() (token, accountID string, chatgpt bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := p.authState()
	chatgpt = isChatGPTMode(auth.Mode)
	if chatgpt {
		return auth.AccessToken, auth.AccountID, true
	}
	// api_key mode: use APIKey as the bearer token.
	if auth.APIKey != "" {
		return auth.APIKey, "", false
	}
	return auth.AccessToken, "", false
}

// currentToken returns the current access token under lock.
func (p *CodexProvider) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authState().AccessToken
}

// reloadTokenFromDisk re-reads the config file and updates the in-memory
// Codex auth state.  This allows the running proxy to pick up tokens
// written by a separate "auth codex" CLI invocation.
func (p *CodexProvider) reloadTokenFromDisk() {
	fresh, err := loadConfig()
	if err != nil {
		log.Printf("[codex] config reload failed: %v", err)
		return
	}
	new := &fresh.Providers.Codex.Auth
	old := p.authState()
	if new.AccessToken != "" && new.AccessToken != old.AccessToken {
		log.Printf("[codex] detected new token on disk — reloading")
		old.AccessToken = new.AccessToken
		old.RefreshToken = new.RefreshToken
		old.ExpiresAt = new.ExpiresAt
		old.AccountID = new.AccountID
	}
}

// reloadFromOfficialStore reads the official Codex auth store and
// merges fresh credentials into the in-memory auth state.
func (p *CodexProvider) reloadFromOfficialStore() {
	official, err := loadOfficialCodexAuth()
	if err != nil {
		log.Printf("[codex] official store load failed: %v", err)
		return
	}
	if official == nil {
		return
	}
	auth := p.authState()
	if official.AccessToken != "" && official.AccessToken != auth.AccessToken {
		log.Printf("[codex] loaded fresh token from official store")
		auth.AccessToken = official.AccessToken
		auth.RefreshToken = official.RefreshToken
		auth.AccountID = official.AccountID
	} else if auth.AccountID == "" && official.AccountID != "" {
		auth.AccountID = official.AccountID
	}
}

// EnsureAuthenticated ensures a valid Codex credential is available.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	auth := p.authState()

	// API key mode — nothing to refresh.
	if isAPIKeyMode(auth.Mode) {
		if auth.APIKey != "" {
			return nil
		}
		return fmt.Errorf("no Codex API key — run 'auth codex --api-key' first")
	}

	// ChatGPT mode: try official store first, then proxy config.
	if auth.AccessToken == "" {
		p.reloadFromOfficialStore()
		if auth.AccessToken == "" {
			p.reloadTokenFromDisk()
		}
		if auth.AccessToken == "" {
			return fmt.Errorf("no Codex token — run 'auth codex' or 'codex auth login'")
		}
	}

	// Ensure we have an account ID (required for ChatGPT backend).
	if auth.AccountID == "" {
		p.reloadFromOfficialStore()
		if auth.AccountID == "" {
			log.Printf("[codex] warning: no account_id found; ChatGPT requests may fail")
		}
	}

	// Check expiry and refresh if needed.
	now := time.Now().Unix()
	if auth.ExpiresAt > 0 {
		remaining := auth.ExpiresAt - now
		threshold := int64(300)
		if remaining <= threshold {
			log.Printf("[codex] token expiring in %ds, refreshing...", remaining)
			if err := codexRefreshToken(auth, p.save); err != nil {
				log.Printf("[codex] refresh failed: %v", err)
				// Try reloading from official store in case CLI refreshed.
				p.reloadFromOfficialStore()
				if auth.AccessToken == "" {
					return fmt.Errorf("codex token expired and refresh failed: %w", err)
				}
			} else {
				log.Printf("[codex] token refreshed, new expiry in %ds",
					auth.ExpiresAt-time.Now().Unix())
			}
		} else {
			log.Printf("[codex] token valid, expires in %dm%ds",
				remaining/60, remaining%60)
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

// fetchModels returns the known-models list (device_code JWT lacks
// api.model.read scope, so we never call the upstream /models endpoint).
func (p *CodexProvider) fetchModels(_ context.Context) (*ModelList, error) {
	return p.knownModelList(), nil
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

// ProxyRequest proxies chat or embeddings requests to the upstream API.
//
// For ChatGPT mode: sends to {chatgpt_base_url}/responses using the
// Responses API format, with chatgpt-account-id header.
// For API key mode: sends to api.openai.com/v1/responses (or chat/completions
// as fallback).
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

	token, accountID, chatgptMode := p.authCredentials()

	// Embeddings: only available in platform (api_key) mode.
	if cap == CapabilityEmbeddings {
		if chatgptMode {
			return fmt.Errorf("codex embeddings not supported in ChatGPT mode")
		}
		return p.proxyClassic(ctx, w, r, body, "/embeddings", token, false)
	}

	// Chat: convert to Responses API format.
	responsesBody, err := chatToResponsesBody(body)
	if err != nil {
		log.Printf("[codex] chat→responses conversion failed: %v — falling back to classic", err)
		if chatgptMode {
			return fmt.Errorf("codex chat conversion failed: %w", err)
		}
		return p.proxyClassic(ctx, w, r, body, "/chat/completions", token, false)
	}

	// Build upstream URL based on mode.
	var upstreamURL string
	if chatgptMode {
		upstreamURL = p.chatgptBaseURL() + "responses"
	} else {
		upstreamURL = defaultPlatformBaseURL + "/responses"
	}

	streaming := isStreamingRequest(body)
	log.Printf("[codex] proxying chat → %s (body %d bytes, stream=%v, chatgpt=%v)",
		upstreamURL, len(responsesBody), streaming, chatgptMode)

	req, err := http.NewRequestWithContext(ctx, "POST",
		upstreamURL, bytes.NewBuffer(responsesBody))
	if err != nil {
		return err
	}
	if chatgptMode {
		setChatGPTHeaders(req, token, accountID)
	} else {
		setPlatformHeaders(req, token)
	}

	resp, err := makeRequestWithRetry(sharedHTTPClient, req, responsesBody)
	if err != nil {
		log.Printf("[codex] upstream error: %v", err)
		p.cb.onFailure()
		return err
	}
	defer resp.Body.Close()

	log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 500 {
		p.cb.onSuccess()
	} else {
		log.Printf("[codex] upstream 5xx error — circuit breaker failure")
		p.cb.onFailure()
	}

	return handleResponsesAPIResponse(w, resp)
}

// proxyClassic sends a request to a classic OpenAI endpoint (embeddings,
// or chat/completions as a fallback).  Only used for api_key mode.
func (p *CodexProvider) proxyClassic(
	ctx context.Context,
	w http.ResponseWriter,
	_ *http.Request,
	body []byte,
	path string,
	token string,
	chatgptMode bool,
) error {
	var upstreamURL string
	if chatgptMode {
		upstreamURL = p.chatgptBaseURL() + strings.TrimPrefix(path, "/")
	} else {
		upstreamURL = defaultPlatformBaseURL + path
	}
	log.Printf("[codex] proxying classic → %s (body %d bytes)", upstreamURL, len(body))

	req, err := http.NewRequestWithContext(ctx, "POST",
		upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	if chatgptMode {
		setChatGPTHeaders(req, token, "")
	} else {
		setPlatformHeaders(req, token)
	}

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
		peekBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("[codex] upstream %d response: %s",
			resp.StatusCode, string(peekBody))
		resp.Body = io.NopCloser(
			io.MultiReader(bytes.NewReader(peekBody), resp.Body))
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
	if auth.ExpiresAt > 0 {
		authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
	}
	hasRefresh := auth.RefreshToken != ""
	return ProviderHealth{Authenticated: authenticated, CanRefresh: hasRefresh}
}
