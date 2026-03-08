// router.go — ModelRouter resolves (model, capability) → provider + upstream model.
package main

import (
	"context"
	"fmt"
	"strings"
)

// RouteResult is the resolved routing decision for a request.
type RouteResult struct {
	Provider      Provider
	ResolvedModel string // model ID to send to this provider's upstream
}

// ModelRouter maps (requestedModel, capability) to the correct provider.
type ModelRouter struct {
	registry *ProviderRegistry
	routing  RoutingConfig
}

// NewModelRouter constructs a ModelRouter wired to the given registry and routing config.
func NewModelRouter(registry *ProviderRegistry, routing RoutingConfig) *ModelRouter {
	return &ModelRouter{registry: registry, routing: routing}
}

// Resolve returns the provider and upstream model for (requestedModel, capability).
// Resolution order:
//  1. routing.model_map (explicit rules, highest priority)
//  2. Auto-discover by scanning provider catalogs
//  3. Fall back to routing.default_provider when no match found
func (r *ModelRouter) Resolve(ctx context.Context, requestedModel string, cap Capability) (*RouteResult, error) {
	model := requestedModel
	if model == "" {
		model = r.routing.DefaultModel
	}

	// 1. Explicit model_map takes highest priority.
	if entry, ok := r.routing.ModelMap[model]; ok {
		p, err := r.registry.Get(ProviderID(entry.Provider))
		if err != nil {
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = model
		}
		return &RouteResult{Provider: p, ResolvedModel: upstream}, nil
	}

	// 2. Auto-discover from provider catalogs (uses cached lists; no blocking remote call here).
	candidates := r.discoverFromCatalogs(ctx, model, cap)

	switch len(candidates) {
	case 0:
		return r.defaultProviderRoute(model)
	case 1:
		return &candidates[0], nil
	default:
		return nil, fmt.Errorf(
			"model %q is ambiguous: found in %d providers; add an explicit routing.model_map entry",
			model, len(candidates),
		)
	}
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

func (r *ModelRouter) defaultProviderRoute(model string) (*RouteResult, error) {
	defID := ProviderID(r.routing.DefaultProvider)
	if defID == "" {
		defID = ProviderCopilot
	}
	p, err := r.registry.Get(defID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found in any provider and default provider %q is unavailable", model, defID)
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
