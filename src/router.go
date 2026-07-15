// router.go — ModelRouter resolves (model, capability) → provider + upstream model.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// RouteResult is the resolved routing decision for a request.
type RouteResult struct {
	Provider      Provider
	ResolvedModel string // bare upstream model ID, without provider prefix
}

// ModelRouter maps (requestedModel, capability) to a provider.
type ModelRouter struct {
	registry *ProviderRegistry
	routing  RoutingConfig
}

func NewModelRouter(registry *ProviderRegistry, routing RoutingConfig) *ModelRouter {
	if routing.ModelMap == nil {
		routing.ModelMap = make(map[string]ModelMapEntry)
	}
	return &ModelRouter{registry: registry, routing: routing}
}

// Resolve order:
//  1. explicit model_map
//  2. explicit provider prefix
//  3. provider catalog discovery
//  4. default provider when the request omitted model
func (r *ModelRouter) Resolve(ctx context.Context, requestedModel string, capability Capability) (*RouteResult, error) {
	model := requestedModel
	usedDefaultModel := model == ""
	if model == "" {
		log.Printf("[router] no model in request, using default=%q", r.routing.DefaultModel)
		model = r.routing.DefaultModel
	}

	bareModel, prefixProvider, hasPrefix := StripModelPrefix(model)

	entry, mapped := r.routing.ModelMap[model]
	if !mapped && hasPrefix {
		entry, mapped = r.routing.ModelMap[bareModel]
	}
	if mapped {
		provider, err := r.registry.Get(ProviderID(entry.Provider))
		if err != nil {
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		if !hasCapability(provider, capability) {
			return nil, fmt.Errorf("model %q maps to provider %q, which does not support capability %q", model, provider.ID(), capability)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = bareModel
		}
		log.Printf("[router] model_map hit: %q → provider=%s upstream=%q", model, provider.ID(), upstream)
		return &RouteResult{Provider: provider, ResolvedModel: upstream}, nil
	}

	if hasPrefix {
		return r.resolveWithPrefix(ctx, model, bareModel, prefixProvider, capability)
	}

	candidates := r.discoverFromCatalogs(ctx, model, capability)
	switch len(candidates) {
	case 0:
		if !usedDefaultModel {
			return nil, fmt.Errorf("model %q is unavailable for capability %q", model, capability)
		}
		return r.defaultProviderRoute(model, capability)
	case 1:
		log.Printf("[router] catalog match: %q → provider=%s", model, candidates[0].Provider.ID())
		return &candidates[0], nil
	default:
		providers := make([]string, len(candidates))
		for i := range candidates {
			providers[i] = string(candidates[i].Provider.ID())
		}
		return nil, fmt.Errorf(
			"model %q is ambiguous: found in %d providers %v; use a provider-prefixed model ID or add routing.model_map",
			model, len(candidates), providers,
		)
	}
}

func (r *ModelRouter) resolveWithPrefix(ctx context.Context, fullModel, bareModel string, providerID ProviderID, capability Capability) (*RouteResult, error) {
	provider, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q: provider %q from prefix is not registered", fullModel, providerID)
	}
	if !hasCapability(provider, capability) {
		return nil, fmt.Errorf("model %q: provider %q does not support capability %q", fullModel, providerID, capability)
	}
	models, listErr := provider.ListModels(ctx)
	if listErr == nil && models != nil {
		for _, model := range models.Data {
			if strings.EqualFold(model.ID, bareModel) {
				log.Printf("[router] prefix-routed: %q → provider=%s upstream=%q", fullModel, provider.ID(), bareModel)
				return &RouteResult{Provider: provider, ResolvedModel: bareModel}, nil
			}
		}
	}
	return nil, fmt.Errorf("model %q is unavailable for capability %q from provider %q", fullModel, capability, providerID)
}

func (r *ModelRouter) discoverFromCatalogs(ctx context.Context, model string, capability Capability) []RouteResult {
	var found []RouteResult
	for _, provider := range r.registry.All() {
		if !hasCapability(provider, capability) {
			continue
		}
		models, err := provider.ListModels(ctx)
		if err != nil || models == nil {
			continue
		}
		for _, candidate := range models.Data {
			if strings.EqualFold(candidate.ID, model) {
				found = append(found, RouteResult{Provider: provider, ResolvedModel: model})
				break
			}
		}
	}
	return found
}

func (r *ModelRouter) defaultProviderRoute(model string, capability Capability) (*RouteResult, error) {
	providerID := ProviderID(r.routing.DefaultProvider)
	if providerID == "" {
		providerID = ProviderCopilot
	}
	provider, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found and default provider %q is unavailable", model, providerID)
	}
	if !hasCapability(provider, capability) {
		return nil, fmt.Errorf("default provider %q does not support capability %q", providerID, capability)
	}
	return &RouteResult{Provider: provider, ResolvedModel: model}, nil
}

func hasCapability(provider Provider, capability Capability) bool {
	for _, supported := range provider.Capabilities() {
		if supported == capability {
			return true
		}
	}
	return false
}
