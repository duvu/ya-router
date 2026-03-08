package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestModelExtractionAndPatchIntegration tests extractModelFromBody
// followed by patchBodyModel as a round-trip.
func TestModelExtractionAndPatchIntegration(t *testing.T) {
	original := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`

	model := extractModelFromBody([]byte(original))
	if model != "gpt-4" {
		t.Fatalf("extractModelFromBody = %q, want gpt-4", model)
	}

	patched := patchBodyModel([]byte(original), "gpt-5-mini")

	newModel := extractModelFromBody(patched)
	if newModel != "gpt-5-mini" {
		t.Errorf("after patch: model = %q, want gpt-5-mini", newModel)
	}

	// Verify messages are preserved
	if !strings.Contains(string(patched), `"content":"hi"`) {
		t.Errorf("message content lost after patch")
	}
}

// TestModelsEndpointConsistency tests the /v1/models endpoint returns
// models from providers and respects allowed_models filtering.
func TestModelsEndpointConsistency(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
			{ID: "gpt-4.1", Object: "model", OwnedBy: "openai"},
			{ID: "gpt-5-mini", Object: "model", OwnedBy: "openai"},
		}},
	})

	cfg := defaultConfig()
	cfg.Providers.Copilot.AllowedModels = []string{
		"gpt-4", "gpt-4.1", "gpt-5-mini",
	}

	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var ml ModelList
	if err := json.NewDecoder(rec.Body).Decode(&ml); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(ml.Data) == 0 {
		t.Fatal("expected at least one model, got 0")
	}

	ids := map[string]bool{}
	for _, m := range ml.Data {
		ids[m.ID] = true
	}

	if !ids["gpt-5-mini"] {
		t.Errorf("gpt-5-mini not in response; got %v", ids)
	}
}

// TestModelsEndpointAggregatesMultipleProviders verifies the models
// endpoint merges models from copilot and codex providers.
func TestModelsEndpointAggregatesMultipleProviders(t *testing.T) {
	registry := NewProviderRegistry()

	copilotModels := []Model{
		{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
	}
	codexModels := []Model{
		{ID: "o3-mini", Object: "model", OwnedBy: "openai"},
	}

	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: copilotModels},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: codexModels},
	})

	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var ml ModelList
	json.NewDecoder(rec.Body).Decode(&ml)

	if len(ml.Data) != 2 {
		t.Fatalf("expected 2 models (aggregated), got %d", len(ml.Data))
	}

	ids := map[string]bool{}
	for _, m := range ml.Data {
		ids[m.ID] = true
	}

	if !ids["gpt-4"] || !ids["o3-mini"] {
		t.Errorf("expected both gpt-4 and o3-mini, got %v", ids)
	}
}

// TestModelsEndpointEmptyWhenNoAuth verifies that the models endpoint
// returns an empty list when no providers are authenticated.
func TestModelsEndpointEmptyWhenNoAuth(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.ShowUnavailableModels = false

	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var ml ModelList
	json.NewDecoder(rec.Body).Decode(&ml)

	if len(ml.Data) != 0 {
		t.Errorf("expected empty models list, got %d models", len(ml.Data))
	}
}

// TestProviderRegistryIntegration tests the ProviderRegistry lifecycle.
func TestProviderRegistryIntegration(t *testing.T) {
	registry := NewProviderRegistry()

	if got := len(registry.All()); got != 0 {
		t.Fatalf("new registry has %d providers, want 0", got)
	}

	p1 := &mockProvider{id: ProviderCopilot, name: "Copilot"}
	p2 := &mockProvider{id: ProviderCodex, name: "Codex"}

	registry.Register(p1)
	registry.Register(p2)

	if got := len(registry.All()); got != 2 {
		t.Fatalf("registry has %d providers, want 2", got)
	}

	got, err := registry.Get(ProviderCopilot)
	if err != nil || got.ID() != ProviderCopilot {
		t.Errorf("Get(copilot) failed: %v", err)
	}

	_, err = registry.Get("nonexistent")
	if err == nil {
		t.Errorf("Get(nonexistent) should return error")
	}
}

// TestModelRouterResolve tests the model router resolution logic.
func TestModelRouterResolve(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat, CapabilityEmbeddings},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
			{ID: "gpt-5-mini", Object: "model"},
		}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "o3-mini", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCopilot)
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"o3-mini": {Provider: "codex"},
	}

	router := NewModelRouter(registry, cfg.Routing)

	tests := []struct {
		name    string
		model   string
		cap     Capability
		wantPID ProviderID
		wantErr bool
	}{
		{
			name:    "explicit model_map hit",
			model:   "o3-mini",
			cap:     CapabilityChat,
			wantPID: ProviderCodex,
		},
		{
			name:    "default provider fallback",
			model:   "gpt-4",
			cap:     CapabilityChat,
			wantPID: ProviderCopilot,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := router.Resolve(context.Background(), tt.model, tt.cap)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve(%q, %q) error = %v, wantErr %v",
					tt.model, tt.cap, err, tt.wantErr)
			}
			if err == nil && result.Provider.ID() != tt.wantPID {
				t.Errorf("Resolve(%q, %q) = %q, want %q",
					tt.model, tt.cap, result.Provider.ID(), tt.wantPID)
			}
		})
	}
}

// TestConfigDefaultAuth verifies default auth config consistency.
func TestConfigDefaultAuth(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Providers.Copilot.Auth.Mode != "device_code" {
		t.Errorf("Copilot auth mode = %q, want device_code",
			cfg.Providers.Copilot.Auth.Mode)
	}
	if cfg.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex auth mode = %q, want device_code",
			cfg.Providers.Codex.Auth.Mode)
	}
}

// TestIsChatGPTModeBackwardCompat verifies backward compatibility
// with the old chatgpt_device_auth and device_code mode names.
func TestIsChatGPTModeBackwardCompat(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"device_code", true},
		{"chatgpt_device_auth", true},
		{"chatgpt", true},
		{"api_key", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := isChatGPTMode(tt.mode); got != tt.want {
				t.Errorf("isChatGPTMode(%q) = %v, want %v",
					tt.mode, got, tt.want)
			}
		})
	}
}

// TestHealthHandler verifies the /healthz endpoint.
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	healthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp["status"] != "ok" {
		t.Errorf("health status = %q, want ok", resp["status"])
	}
}
