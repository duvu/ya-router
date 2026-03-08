// models.go — unified /v1/models handler and shared model utilities.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// filterAllowedModels returns a copy of modelList containing only models that
// appear in allowedModels.  An empty (nil or zero-length) allowedModels slice
// means "allow all".
// If the allowedModels list is set but no models match, a synthetic entry for
// each allowed ID is returned so clients can still see the allowed model names.
func filterAllowedModels(modelList *ModelList, allowedModels []string) *ModelList {
	if len(allowedModels) == 0 {
		return modelList
	}

	var filtered []Model
	for _, m := range modelList.Data {
		if isModelAllowed(m.ID, allowedModels) {
			filtered = append(filtered, m)
		}
	}

	if len(filtered) == 0 {
		// Synthesise entries for each allowed ID so callers aren't left empty.
		for _, id := range allowedModels {
			filtered = append(filtered, Model{
				ID:      id,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: "openai",
			})
		}
	}
	return &ModelList{Object: "list", Data: filtered}
}

// modelsHandler returns an HTTP handler that merges model lists from all
// enabled providers and writes the combined list as JSON.
// Models are always loaded dynamically from provider APIs — no hardcoded
// fallback is used.
func modelsHandler(registry *ProviderRegistry, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		seen := make(map[string]bool)
		var allModels []Model

		// 1. Collect models from each provider's upstream API.
		for _, p := range registry.All() {
			h := p.Health(ctx)
			if !h.Authenticated && !cfg.Routing.ShowUnavailableModels {
				continue
			}
			ml, err := p.ListModels(ctx)
			if err != nil {
				log.Printf("modelsHandler: provider %s list error: %v", p.ID(), err)
				continue
			}
			if ml != nil {
				for _, m := range ml.Data {
					if !seen[m.ID] {
						seen[m.ID] = true
						allModels = append(allModels, m)
					}
				}
			}
		}

		// 2. Ensure every model in routing.model_map is represented.
		for modelID := range cfg.Routing.ModelMap {
			if !seen[modelID] {
				seen[modelID] = true
				allModels = append(allModels, Model{
					ID:      modelID,
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "openai",
				})
			}
		}

		resp := &ModelList{Object: "list", Data: allModels}
		if allModels == nil {
			resp.Data = []Model{}
		}
		log.Printf("modelsHandler: returning %d models", len(resp.Data))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("modelsHandler: encode error: %v", err)
		}
	}
}
