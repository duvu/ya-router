package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	cachedModels *ModelList
	modelsMutex  sync.RWMutex
	modelsLoaded bool
	modelsCacheExpiresAt time.Time
)

// fetchModelsFromCopilotAPI tries to get models directly from GitHub Copilot API
func fetchModelsFromCopilotAPI(token string) (*ModelList, error) {
	// GitHub Copilot model list endpoint is /models (not /v1/models) and requires
	// IDE auth headers similar to chat requests.
	req, err := http.NewRequest("GET", copilotAPIBase+"/models", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	// Required IDE headers (same spirit as chat completions)
	req.Header.Set("Editor-Version", "vscode/1.99.3")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("Openai-Intent", "conversation-edits")
	req.Header.Set("X-Initiator", "user")

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("copilot API returned status %d", resp.StatusCode)
	}

	var modelList ModelList
	if err := json.NewDecoder(resp.Body).Decode(&modelList); err != nil {
		return nil, err
	}

	return &modelList, nil
}

// filterAllowedModels filters the model list to only include allowed models
func filterAllowedModels(modelList *ModelList, cfg *Config) *ModelList {
	// If no allowed models configured, return all models
	if len(cfg.AllowedModels) == 0 {
		return modelList
	}

	var filteredModels []Model
	for _, model := range modelList.Data {
		if isModelAllowed(model.ID, cfg) {
			filteredModels = append(filteredModels, model)
		}
	}

	// If no models match the allowed list, add the default models
	if len(filteredModels) == 0 {
		for _, allowedModelID := range cfg.AllowedModels {
			filteredModels = append(filteredModels, Model{
				ID:      allowedModelID,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: "openai", // Default to openai for allowed models
			})
		}
	}

	return &ModelList{
		Object: "list",
		Data:   filteredModels,
	}
}

// getDefaultModels provides a fallback list of models (defined in main.go)
func getDefaultModels() []Model {
	// Hardcoded fallback list used only when Copilot API models cannot be fetched.
	return []Model{
		// GitHub Copilot (OpenAI-compatible)
		{ID: "gpt-4o", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		{ID: "gpt-4.1", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		{ID: "gpt-5-mini", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		{ID: "o3", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		{ID: "o3-mini", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		{ID: "o4-mini", Object: "model", Created: time.Now().Unix(), OwnedBy: "openai"},
		// Claude (Anthropic)
		{ID: "claude-3.5-sonnet", Object: "model", Created: time.Now().Unix(), OwnedBy: "anthropic"},
		{ID: "claude-3.7-sonnet", Object: "model", Created: time.Now().Unix(), OwnedBy: "anthropic"},
		{ID: "claude-3.7-sonnet-thought", Object: "model", Created: time.Now().Unix(), OwnedBy: "anthropic"},
		{ID: "claude-opus-4", Object: "model", Created: time.Now().Unix(), OwnedBy: "anthropic"},
		{ID: "claude-sonnet-4", Object: "model", Created: time.Now().Unix(), OwnedBy: "anthropic"},
		// Gemini (Google)
		{ID: "gemini-2.5-pro", Object: "model", Created: time.Now().Unix(), OwnedBy: "google"},
		{ID: "gemini-2.0-flash-001", Object: "model", Created: time.Now().Unix(), OwnedBy: "google"},
	}
}

func getCachedModelsIfFresh(now time.Time) (*ModelList, bool) {
	modelsMutex.RLock()
	defer modelsMutex.RUnlock()
	if !modelsLoaded || cachedModels == nil {
		return nil, false
	}
	if modelsCacheExpiresAt.IsZero() || now.After(modelsCacheExpiresAt) {
		return nil, false
	}
	return cachedModels, true
}

func setModelsCache(modelList *ModelList, ttl time.Duration) {
	modelsMutex.Lock()
	defer modelsMutex.Unlock()
	cachedModels = modelList
	modelsLoaded = true
	modelsCacheExpiresAt = time.Now().Add(ttl)
}

func modelsHandler(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Use request coalescing for identical concurrent requests
		requestKey := modelsCoalescingCache.getRequestKey("GET", "/v1/models", nil)

		result := modelsCoalescingCache.CoalesceRequest(requestKey, func() interface{} {
			now := time.Now()
			if modelList, ok := getCachedModelsIfFresh(now); ok {
				return modelList
			}

			log.Printf("Refreshing models cache from GitHub Copilot API...")

			// If we don't have a token (e.g., tests), return fallback list.
			if cfg.CopilotToken == "" {
				fallback := &ModelList{Object: "list", Data: getDefaultModels()}
				// Short cache for fallback to avoid sticking forever if token appears.
				setModelsCache(fallback, 5*time.Minute)
				return fallback
			}

			modelList, err := fetchModelsFromCopilotAPI(cfg.CopilotToken)
			if err != nil {
				log.Printf("Failed to fetch models from GitHub Copilot API: %v, using default models", err)
				fallback := &ModelList{Object: "list", Data: getDefaultModels()}
				setModelsCache(fallback, 5*time.Minute)
				return fallback
			}

			setModelsCache(modelList, 24*time.Hour)
			log.Printf("Loaded and cached %d models (ttl=24h)", len(modelList.Data))
			return modelList
		})

		modelList := result.(*ModelList)
		log.Printf("Returning models (%d models)", len(modelList.Data))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(modelList); err != nil {
			log.Printf("Error encoding models response: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
	}
}
