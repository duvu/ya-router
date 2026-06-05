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
	ResolvedModel string // model ID to send to this provider's upstream (always bare, no prefix)
}

// ModelRouter maps (requestedModel, capability) to the correct provider.
type ModelRouter struct {
	registry *ProviderRegistry
	routing  RoutingConfig
}

// NewModelRouter constructs a ModelRouter wired to the given registry and routing config.
func NewModelRouter(registry *ProviderRegistry, routing RoutingConfig) *ModelRouter {
	if routing.ModelMap == nil {
		routing.ModelMap = make(map[string]ModelMapEntry)
	}
	return &ModelRouter{registry: registry, routing: routing}
}

// Resolve returns the provider and upstream model for (requestedModel, capability).
// Resolution order:
//  1. routing.model_map (explicit rules, highest priority) — checked with both
//     the full key (prefixed or bare, as supplied) and a bare fallback.
//  2. If the requested model carries a known provider prefix (gc-/oc-), only
//     that provider's catalog is searched and the prefix is stripped before
//     forwarding.
//  3. Auto-discover by scanning all provider catalogs (bare ID).
//  4. Fall back to routing.default_provider when no model was specified.
func (r *ModelRouter) Resolve(ctx context.Context, requestedModel string, cap Capability) (*RouteResult, error) {
	model := requestedModel
	usedDefaultModel := model == ""
	if model == "" {
		log.Printf("[router] no model in request, using default=%q", r.routing.DefaultModel)
		model = r.routing.DefaultModel
	}

	// Strip prefix early so we have both the full key and the bare ID.
	bareModel, prefixProvider, hasPrefx := StripModelPrefix(model)

	// 1. Explicit model_map takes highest priority.
	//    Try the full key first (supports both "gc-gpt-4o" and "gpt-4o" as keys).
	//    Fall back to the bare ID when the prefixed form is not in the map.
	mapKey := model
	entry, ok := r.routing.ModelMap[mapKey]
	if !ok && hasPrefx {
		mapKey = bareModel
		entry, ok = r.routing.ModelMap[mapKey]
	}
	if ok {
		p, err := r.registry.Get(ProviderID(entry.Provider))
		if err != nil {
			log.Printf("[router] model_map hit for %q → provider %q NOT FOUND", model, entry.Provider)
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = bareModel
		}
		log.Printf("[router] model_map hit: %q → provider=%s upstream=%q", model, p.ID(), upstream)
		return &RouteResult{Provider: p, ResolvedModel: upstream}, nil
	}

	// 2. If a provider prefix was supplied, restrict the search to that provider only.
	if hasPrefx {
		return r.resolveWithPrefix(ctx, model, bareModel, prefixProvider, cap)
	}

	// 3. Auto-discover from provider catalogs (uses cached lists; no blocking remote call here).
	candidates := r.discoverFromCatalogs(ctx, model, cap)

	switch len(candidates) {
	case 0:
		if !usedDefaultModel {
			log.Printf("[router] model %q unavailable for capability=%s", model, cap)
			return nil, fmt.Errorf("model %q is unavailable for capability %q", model, cap)
		}
		log.Printf("[router] model %q not in any catalog, falling back to default provider", model)
		return r.defaultProviderRoute(model, cap)
	case 1:
		log.Printf("[router] catalog match: %q → provider=%s", model, candidates[0].Provider.ID())
		return &candidates[0], nil
	default:
		providerNames := make([]string, len(candidates))
		for i, c := range candidates {
			providerNames[i] = string(c.Provider.ID())
		}
		log.Printf("[router] model %q AMBIGUOUS: found in providers %v — use a prefixed model ID (e.g. gc-%s or oc-%s) or add a routing.model_map entry", model, providerNames, model, model)
		return nil, fmt.Errorf(
			"model %q is ambiguous: found in %d providers %v; use a provider-prefixed model ID (e.g. gc-%s, oc-%s) or add a routing.model_map entry",
			model, len(candidates), providerNames, model, model,
		)
	}
}

// resolveWithPrefix resolves a prefixed model ID against the specific provider
// indicated by the prefix.  Returns an error if the model is unavailable from
// that provider.
func (r *ModelRouter) resolveWithPrefix(ctx context.Context, fullModel, bareModel string, provID ProviderID, cap Capability) (*RouteResult, error) {
	p, err := r.registry.Get(provID)
	if err != nil {
		return nil, fmt.Errorf("model %q: provider %q (from prefix) is not registered", fullModel, provID)
	}
	if !hasCapability(p, cap) {
		return nil, fmt.Errorf("model %q: provider %q does not support capability %q", fullModel, provID, cap)
	}
	// Check provider catalog for the bare model ID.
	models, listErr := p.ListModels(ctx)
	if listErr == nil && models != nil {
		for _, m := range models.Data {
			if strings.EqualFold(m.ID, bareModel) {
				log.Printf("[router] prefix-routed: %q → provider=%s upstream=%q", fullModel, p.ID(), bareModel)
				return &RouteResult{Provider: p, ResolvedModel: bareModel}, nil
			}
		}
	}
	// Model not in catalog — return explicit error rather than fallback.
	log.Printf("[router] model %q not found in %s catalog", bareModel, provID)
	return nil, fmt.Errorf("model %q is unavailable for capability %q from provider %q", fullModel, cap, provID)
}

func (r *ModelRouter) discoverFromCatalogs(ctx context.Context, model string, cap Capability) []RouteResult {
	var found []RouteResult
	for _, p := range r.registry.All() {
		if !hasCapability(p, cap) {
			continue
		}
		models, err := p.ListModels(ctx)
		if err != nil || models == nil {
			continue
		}
		for _, m := range models.Data {
			if strings.EqualFold(m.ID, model) {
				found = append(found, RouteResult{Provider: p, ResolvedModel: model})
				break
			}
		}
	}
	return found
}

func (r *ModelRouter) defaultProviderRoute(model string, cap Capability) (*RouteResult, error) {
	defID := ProviderID(r.routing.DefaultProvider)
	if defID == "" {
		defID = ProviderCopilot
	}
	p, err := r.registry.Get(defID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found in any provider and default provider %q is unavailable", model, defID)
	}
	if !hasCapability(p, cap) {
		return nil, fmt.Errorf("default provider %q does not support capability %q", defID, cap)
	}
	return &RouteResult{Provider: p, ResolvedModel: model}, nil
}

// hasCapability reports whether provider p supports the given capability.
func hasCapability(p Provider, cap Capability) bool {
	for _, c := range p.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}
