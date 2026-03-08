// codex_provider.go — OpenAI Codex backend provider implementation.
// Auth tokens are persisted in the project config folder alongside
// Copilot tokens.  The token value itself is never logged.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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

// jwtClaims extracts organization_id and project_id from a JWT access
// token without signature verification (we only need the claims).
func jwtClaims(token string) (orgID, projectID string) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) < 2 {
		return
	}
	// base64url decode the payload.
	payload := parts[1]
	if m := len(payload) % 4; m != 0 {
		payload += strings.Repeat("=", 4-m)
	}
	b, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return
	}
	var claims struct {
		OrgID     string `json:"organization_id"`
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(b, &claims) == nil {
		orgID = claims.OrgID
		projectID = claims.ProjectID
	}
	return
}

// setCodexHeaders sets the standard headers for Codex API requests,
// including the openai-beta flag and org/project from the JWT.
func setCodexHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("OpenAI-Beta", "codex-2025-05-19")
	if orgID, projectID := jwtClaims(token); orgID != "" {
		req.Header.Set("OpenAI-Organization", orgID)
		if projectID != "" {
			req.Header.Set("OpenAI-Project", projectID)
		}
	}
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
	}
}

// EnsureAuthenticated ensures a valid Codex credential is available.
// Uses device_code mode: check expiry, attempt refresh, fall back to
// re-authentication error.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	auth := p.authState()

	// device_code mode — token stored in config.
	if auth.AccessToken == "" {
		// Maybe a separate 'auth codex' process wrote a token.
		p.reloadTokenFromDisk()
		if auth.AccessToken == "" {
			log.Printf("[codex] no access token available")
			return fmt.Errorf("no Codex token — run 'auth codex' first")
		}
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

// ProxyRequest proxies chat or embeddings requests to the OpenAI API.
// For chat requests the Responses API (/v1/responses) is used because the
// device_code token (ChatGPT Plus) is only authorised there; the adapter
// translates between Chat Completions and Responses formats on-the-fly.
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

	// Embeddings still use the classic endpoint.
	if cap == CapabilityEmbeddings {
		return p.proxyClassic(ctx, w, r, body, "/embeddings")
	}

	// Chat: convert to Responses API format.
	responsesBody, err := chatToResponsesBody(body)
	if err != nil {
		log.Printf("[codex] chat→responses conversion failed: %v — falling back to classic", err)
		return p.proxyClassic(ctx, w, r, body, "/chat/completions")
	}

	upstreamURL := codexAPIBase + "/responses"
	streaming := isStreamingRequest(body)
	log.Printf("[codex] proxying chat → %s (body %d bytes, stream=%v)",
		upstreamURL, len(responsesBody), streaming)

	token := p.currentToken()
	req, err := http.NewRequestWithContext(ctx, "POST",
		upstreamURL, bytes.NewBuffer(responsesBody))
	if err != nil {
		return err
	}
	setCodexHeaders(req, token)

	resp, err := makeRequestWithRetry(sharedHTTPClient, req, responsesBody)
	if err != nil {
		log.Printf("[codex] upstream error: %v", err)
		p.cb.onFailure()
		return err
	}
	defer resp.Body.Close()

	log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	// On 401, try reloading token from disk (auth codex may have
	// written a new token) and retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		log.Printf("[codex] 401 — reloading token from disk and retrying")
		p.mu.Lock()
		p.reloadTokenFromDisk()
		newToken := p.authState().AccessToken
		p.mu.Unlock()
		if newToken != token {
			req2, err := http.NewRequestWithContext(ctx, "POST",
				upstreamURL, bytes.NewBuffer(responsesBody))
			if err != nil {
				return err
			}
			setCodexHeaders(req2, newToken)
			resp, err = makeRequestWithRetry(sharedHTTPClient, req2, responsesBody)
			if err != nil {
				p.cb.onFailure()
				return err
			}
			defer resp.Body.Close()
			log.Printf("[codex] retry responded HTTP %d", resp.StatusCode)
		}
	}

	if resp.StatusCode < 500 {
		p.cb.onSuccess()
	} else {
		log.Printf("[codex] upstream 5xx error — circuit breaker failure")
		p.cb.onFailure()
	}

	return handleResponsesAPIResponse(w, resp)
}

// proxyClassic sends a request to a classic OpenAI endpoint (embeddings,
// or chat/completions as a fallback).
func (p *CodexProvider) proxyClassic(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	path string,
) error {
	upstreamURL := codexAPIBase + path
	log.Printf("[codex] proxying classic → %s (body %d bytes)", upstreamURL, len(body))

	token := p.currentToken()
	req, err := http.NewRequestWithContext(ctx, r.Method,
		upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	setCodexHeaders(req, token)

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
		log.Printf("[codex] upstream %d response: %s", resp.StatusCode, string(peekBody))
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
	if auth.ExpiresAt > 0 {
		authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
	}
	hasRefresh := auth.RefreshToken != ""
	return ProviderHealth{Authenticated: authenticated, CanRefresh: hasRefresh}
}
