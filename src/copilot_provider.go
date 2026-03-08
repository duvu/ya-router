// copilot_provider.go — GitHub Copilot backend provider implementation.
package main

import (
"bytes"
"context"
"encoding/json"
"fmt"
"io"
"log"
"net/http"
"sync"
"time"
)

// CopilotProvider implements Provider for the GitHub Copilot backend.
type CopilotProvider struct {
cfg *Config // full config; Copilot auth lives at cfg.Providers.Copilot
mu  sync.Mutex
cb  *CircuitBreaker
mc  *CoalescingCache // ListModels request coalescing
cache *ModelCache     // TTL-based model list cache
}

// NewCopilotProvider constructs a CopilotProvider backed by cfg.
func NewCopilotProvider(cfg *Config) *CopilotProvider {
timeout := time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second
return &CopilotProvider{
cfg:   cfg,
cb:    &CircuitBreaker{state: CircuitClosed, timeout: timeout},
mc:    NewCoalescingCache(),
cache: NewModelCache(defaultModelCacheTTL),
}
}

func (p *CopilotProvider) ID() ProviderID { return ProviderCopilot }
func (p *CopilotProvider) Name() string   { return "GitHub Copilot" }
func (p *CopilotProvider) Capabilities() []Capability {
return []Capability{CapabilityChat, CapabilityEmbeddings}
}

func (p *CopilotProvider) authState() *CopilotAuthState {
return &p.cfg.Providers.Copilot.Auth
}

func (p *CopilotProvider) save() error {
return saveConfig(p.cfg)
}

// EnsureAuthenticated ensures the Copilot bearer token is valid.
func (p *CopilotProvider) EnsureAuthenticated(ctx context.Context) error {
p.mu.Lock()
defer p.mu.Unlock()

auth := p.authState()
if auth.CopilotToken == "" {
return copilotAuthenticate(auth, p.save)
}

now := time.Now().Unix()
threshold := int64(300) // 5 min default
if auth.RefreshIn > 0 {
if t := auth.RefreshIn / 5; t > threshold {
threshold = t
}
}
if auth.ExpiresAt-now <= threshold {
if err := copilotRefreshToken(auth, p.save); err != nil {
log.Printf("Copilot refresh failed, re-authenticating: %v", err)
return copilotAuthenticate(auth, p.save)
}
}
return nil
}

// ListModels returns models from the Copilot API with caching, coalescing, and filtering.
func (p *CopilotProvider) ListModels(ctx context.Context) (*ModelList, error) {
if err := p.EnsureAuthenticated(ctx); err != nil {
return nil, err
}
return p.cache.GetOrFetch(func() (*ModelList, error) {
key := p.mc.getRequestKey("GET", copilotAPIBase+"/models", nil)
result := p.mc.CoalesceRequest(key, func() interface{} {
return p.fetchModelsRaw()
})
if err, ok := result.(error); ok {
return nil, err
}
ml := result.(*ModelList)
filtered := filterAllowedModels(ml, p.cfg.Providers.Copilot.AllowedModels)
// Deduplicate by model ID (upstream may return duplicates).
seen := make(map[string]bool)
var deduped []Model
for _, m := range filtered.Data {
	if !seen[m.ID] {
		seen[m.ID] = true
		deduped = append(deduped, m)
	}
}
filtered.Data = deduped
return filtered, nil
})
}

func (p *CopilotProvider) fetchModelsRaw() interface{} {
	token := p.authState().CopilotToken
	if token == "" {
		return &ModelList{Object: "list", Data: nil}
	}
	req, err := http.NewRequest("GET", copilotAPIBase+"/models", nil)
	if err != nil {
		return err
	}
	p.setCopilotHeaders(req, token, CapabilityChat)
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Copilot /models returned %d", resp.StatusCode)
		return fmt.Errorf("copilot /models: HTTP %d", resp.StatusCode)
	}
	var ml ModelList
	if err := json.NewDecoder(resp.Body).Decode(&ml); err != nil {
		return err
	}
	return &ml
}

// ProxyRequest proxies a chat or embeddings request to the Copilot API.
func (p *CopilotProvider) ProxyRequest(
ctx context.Context,
w http.ResponseWriter,
r *http.Request,
body []byte,
cap Capability,
) error {
if err := p.EnsureAuthenticated(ctx); err != nil {
log.Printf("[copilot] auth failed: %v", err)
return fmt.Errorf("copilot auth: %w", err)
}
if !p.cb.canExecute() {
log.Printf("[copilot] circuit breaker OPEN — rejecting request")
return fmt.Errorf("copilot circuit breaker is open")
}

isEmbed := cap == CapabilityEmbeddings
embedModel := ""
if isEmbed {
body, embedModel = normalizeEmbeddingsRequestBody(body)
}

targetPath := "/chat/completions"
if isEmbed {
targetPath = "/embeddings"
}

upstreamURL := copilotAPIBase + targetPath
log.Printf("[copilot] proxying %s → %s (body %d bytes)", cap, upstreamURL, len(body))

req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewBuffer(body))
if err != nil {
return err
}
p.setCopilotHeaders(req, p.authState().CopilotToken, cap)

resp, err := makeRequestWithRetry(sharedHTTPClient, req, body)
if err != nil {
log.Printf("[copilot] upstream error: %v", err)
p.cb.onFailure()
return err
}
defer resp.Body.Close()

log.Printf("[copilot] upstream responded HTTP %d (Content-Type: %s)",
	resp.StatusCode, resp.Header.Get("Content-Type"))

if resp.StatusCode >= 400 && resp.StatusCode < 500 {
	peekBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	log.Printf("[copilot] upstream %d response: %s", resp.StatusCode, string(peekBody))
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peekBody), resp.Body))
}

if resp.StatusCode < 500 {
p.cb.onSuccess()
} else {
log.Printf("[copilot] upstream 5xx error — circuit breaker failure")
p.cb.onFailure()
}

if isEmbed && resp.Header.Get("Content-Type") != "text/event-stream" {
return writeCopilotEmbeddingsResponse(w, resp, embedModel)
}
return streamResponse(w, resp)
}

func (p *CopilotProvider) setCopilotHeaders(req *http.Request, token string, cap Capability) {
req.Header.Set("Authorization", "Bearer "+token)
req.Header.Set("Content-Type", "application/json")
req.Header.Set("Accept", "application/json")
req.Header.Set("User-Agent", userAgent)
req.Header.Set("Editor-Version", "vscode/1.99.3")
req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
req.Header.Set("Copilot-Integration-Id", "vscode-chat")
req.Header.Set("X-Initiator", "user")
if cap == CapabilityEmbeddings {
req.Header.Set("Openai-Intent", "embeddings")
} else {
req.Header.Set("Openai-Intent", "conversation-edits")
}
}

func writeCopilotEmbeddingsResponse(w http.ResponseWriter, resp *http.Response, embedModel string) error {
body, err := io.ReadAll(resp.Body)
if err != nil {
return err
}
if resp.StatusCode >= 200 && resp.StatusCode < 300 {
body = ensureEmbeddingsResponseCompat(body, embedModel)
}
copyHeaders(w, resp.Header, "Content-Length")
w.Header().Set("Access-Control-Allow-Origin", "*")
w.Header().Set("Access-Control-Allow-Headers", "*")
w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
w.WriteHeader(resp.StatusCode)
_, err = w.Write(body)
return err
}

// Health returns the provider's current authentication state.
func (p *CopilotProvider) Health(_ context.Context) ProviderHealth {
p.mu.Lock()
defer p.mu.Unlock()
auth := p.authState()
hasRefresh := auth.GitHubToken != ""
authenticated := auth.CopilotToken != "" && auth.ExpiresAt > time.Now().Unix()
return ProviderHealth{
Authenticated: authenticated,
CanRefresh:    hasRefresh,
LastRefreshAt: time.Unix(auth.ExpiresAt, 0),
}
}
