package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	cachedModels *ModelList
	modelsMutex  sync.RWMutex
	modelsLoaded bool
)

// ModelsDevResponse represents the structure from models.dev API
type ModelsDevResponse map[string]struct {
	ID     string `json:"id"`
	Models map[string]struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
		OwnedBy     string `json:"owned_by,omitempty"`
	} `json:"models"`
}

// fetchModelsFromCopilotAPI tries to get models directly from GitHub Copilot API
func fetchModelsFromCopilotAPI(token string) (*ModelList, error) {
	req, err := http.NewRequest("GET", "https://api.githubcopilot.com/v1/models", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

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

// fetchModelsFromModelsDev fetches models from models.dev API as fallback
func fetchModelsFromModelsDev() (*ModelList, error) {
	// Use shared HTTP client if available for consistent timeouts
	client := sharedHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest("GET", "https://models.dev/api.json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("models.dev API returned status %d", resp.StatusCode)
	}

	var providers ModelsDevResponse
	if err := json.NewDecoder(resp.Body).Decode(&providers); err != nil {
		return nil, err
	}

	// Extract GitHub Copilot models
	copilotProvider, exists := providers["github-copilot"]
	if !exists {
		return nil, fmt.Errorf("github-copilot provider not found in models.dev")
	}

	var models []Model
	for modelID, modelInfo := range copilotProvider.Models {
		ownedBy := modelInfo.OwnedBy
		if ownedBy == "" {
			// Determine owner based on model name
			if containsAny(modelInfo.Name, []string{"claude", "anthropic"}) {
				ownedBy = "anthropic"
			} else if containsAny(modelInfo.Name, []string{"gpt", "o1", "o3", "o4", "openai"}) {
				ownedBy = "openai"
			} else if containsAny(modelInfo.Name, []string{"gemini", "google"}) {
				ownedBy = "google"
			} else {
				ownedBy = "github-copilot"
			}
		}

		models = append(models, Model{
			ID:      modelID,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: ownedBy,
		})
	}

	return &ModelList{
		Object: "list",
		Data:   models,
	}, nil
}

// containsAny checks if text contains any of the substrings
func containsAny(text string, substrings []string) bool {
	textLower := strings.ToLower(text)
	for _, substr := range substrings {
		if strings.Contains(textLower, strings.ToLower(substr)) {
			return true
		}
	}
	return false
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
	// Models based on actual models.dev GitHub Copilot, Claude, and Gemini entries (as of August 2025)
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

func modelsHandler(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Use request coalescing for identical concurrent requests
		requestKey := modelsCoalescingCache.getRequestKey("GET", "/v1/models", nil)

		result := modelsCoalescingCache.CoalesceRequest(requestKey, func() interface{} {
			// Check cache first
			modelsMutex.RLock()
			if modelsLoaded && cachedModels != nil {
				modelsMutex.RUnlock()
				return filterAllowedModels(cachedModels, cfg)
			}
			modelsMutex.RUnlock()

			// Load models if not cached
			modelsMutex.Lock()
			defer modelsMutex.Unlock()

			// Double-check in case another goroutine loaded while we waited
			if modelsLoaded && cachedModels != nil {
				return filterAllowedModels(cachedModels, cfg)
			}

			log.Printf("Loading models for the first time...")

			// Try models.dev API first (don't hit GitHub Copilot for models list)
			modelList, err := fetchModelsFromModelsDev()
			if err != nil {
				log.Printf("Failed to fetch from models.dev: %v, using default models", err)

				// Ultimate fallback to hardcoded models
				modelList = &ModelList{
					Object: "list",
					Data:   getDefaultModels(),
				}
			}

			// Cache the results
			cachedModels = modelList
			modelsLoaded = true

			log.Printf("Loaded and cached %d models", len(modelList.Data))
			return filterAllowedModels(modelList, cfg)
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
