// Package routing resolves requested models to providers without depending on
// provider implementations or HTTP handlers.
package routing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

// Result is the resolved routing decision for a request.
type Result struct {
	Provider      provider.Provider
	ResolvedModel string
	// Selection is set only when the request resolved through an umbrella
	// (virtual) model. It carries bounded, redacted decision metadata for
	// observability and is nil for explicit routes.
	Selection *SelectionDecision
}

// nowFunc is overridable in tests; production always uses the wall clock.
var nowFunc = time.Now

// lastKnownCatalog is optionally implemented by providers to expose their
// last-known-good model catalog without performing network I/O. Umbrella-model
// availability reads it on the request hot path.
type lastKnownCatalog interface {
	LastKnownModels() (models []string, fetchedAt time.Time, stale bool, ok bool)
}

// allowlistReporter is optionally implemented by providers to expose their
// configured allowlist of bare model IDs for availability evaluation.
type allowlistReporter interface {
	AllowedModelIDs() []string
}

// Router maps a requested model and capability to a provider.
type Router struct {
	registry   *provider.Registry
	routing    configschema.Routing
	generation uint64
	metrics    *Metrics
	cooldowns  *CooldownRegistry
	preferred  *PreferredTargetRegistry
}

// NewRouter builds an isolated model router over registry.
func NewRouter(registry *provider.Registry, config configschema.Routing) *Router {
	return NewRouterWithRegistries(registry, config, nil, nil)
}

// NewRouterWithCooldowns builds a router over a shared cooldown registry. A
// runtime manager shares this registry across immutable snapshot publications.
func NewRouterWithCooldowns(registry *provider.Registry, config configschema.Routing, cooldowns *CooldownRegistry) *Router {
	return NewRouterWithRegistries(registry, config, cooldowns, nil)
}

// NewRouterWithRegistries builds a router over shared cooldown and preferred-target registries.
func NewRouterWithRegistries(registry *provider.Registry, config configschema.Routing, cooldowns *CooldownRegistry, preferred *PreferredTargetRegistry) *Router {
	if config.ModelMap == nil {
		config.ModelMap = make(map[string]configschema.ModelMapEntry)
	}
	if cooldowns == nil {
		cooldowns = NewCooldownRegistry()
	}
	if preferred == nil {
		preferred = NewPreferredTargetRegistry()
	}
	return &Router{registry: registry, routing: config, metrics: NewMetrics(), cooldowns: cooldowns, preferred: preferred}
}

// SetGeneration records the runtime generation this router serves so umbrella
// decision metadata can identify the state used. It is set once at snapshot
// construction, before the router is published, and never mutated afterward.
func (r *Router) SetGeneration(generation uint64) { r.generation = generation }

// Metrics returns the router's umbrella-routing metrics sink.
func (r *Router) Metrics() *Metrics { return r.metrics }

// VirtualModelIDs returns the configured umbrella model IDs. It supports
// /v1/models exposure without leaking the routing configuration.
func (r *Router) VirtualModelIDs() []string {
	ids := make([]string, 0, len(r.routing.VirtualModels))
	for id := range r.routing.VirtualModels {
		ids = append(ids, id)
	}
	return ids
}

// Resolve applies model-map, provider-prefix, catalog, then default routing.
func (r *Router) Resolve(ctx context.Context, requestedModel string, capability provider.Capability) (*Result, error) {
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
		registered, err := r.registry.Get(provider.ID(entry.Provider))
		if err != nil {
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		if !hasCapability(registered, capability) {
			return nil, fmt.Errorf("model %q maps to provider %q, which does not support capability %q", model, registered.ID(), capability)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = bareModel
		}
		log.Printf("[router] model_map hit: %q → provider=%s upstream=%q", model, registered.ID(), upstream)
		return &Result{Provider: registered, ResolvedModel: upstream}, nil
	}

	if hasPrefix {
		return r.resolveWithPrefix(ctx, model, bareModel, prefixProvider, capability)
	}

	if vm, ok := r.routing.VirtualModels[model]; ok {
		return r.resolveVirtualModel(ctx, model, vm, capability)
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
		return nil, fmt.Errorf("model %q is ambiguous: found in %d providers %v; use a provider-prefixed model ID or add routing.model_map", model, len(candidates), providers)
	}
}

func (r *Router) resolveWithPrefix(ctx context.Context, fullModel, bareModel string, providerID provider.ID, capability provider.Capability) (*Result, error) {
	registered, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q: provider %q from prefix is not registered", fullModel, providerID)
	}
	if !hasCapability(registered, capability) {
		return nil, fmt.Errorf("model %q: provider %q does not support capability %q", fullModel, providerID, capability)
	}
	models, listErr := registered.ListModels(ctx)
	if listErr == nil && models != nil {
		for _, model := range models.Data {
			if strings.EqualFold(model.ID, bareModel) {
				log.Printf("[router] prefix-routed: %q → provider=%s upstream=%q", fullModel, registered.ID(), bareModel)
				return &Result{Provider: registered, ResolvedModel: bareModel}, nil
			}
		}
	}
	return nil, fmt.Errorf("model %q is unavailable for capability %q from provider %q", fullModel, capability, providerID)
}

// resolveVirtualModel selects one active target for an umbrella model and
// resolves it to a concrete provider/model. Selection reads a network-free
// availability snapshot; the selected target is resolved directly without an
// extra catalog fetch so exactly one provider is dispatched. If no target is
// active, a typed NoActiveTargetError is returned and no provider is called.
func (r *Router) resolveVirtualModel(ctx context.Context, virtualID string, vm configschema.VirtualModel, capability provider.Capability) (*Result, error) {
	return r.resolveVirtualModelExcluding(ctx, virtualID, vm, capability, nil)
}

// resolveVirtualModelExcluding is the shared implementation that skips any
// target in the exclude set. It is called for the first attempt without
// exclusions and for each failover attempt with the previously tried targets.
func (r *Router) resolveVirtualModelExcluding(ctx context.Context, virtualID string, vm configschema.VirtualModel, capability provider.Capability, exclude map[string]struct{}) (*Result, error) {
	orderedVM := vm
	// The legacy priority strategy preserves the last-successful-target
	// optimization introduced by #97. quota_priority deliberately ignores that
	// memory so an expired higher-priority quota cooldown restores configured
	// order on the next request.
	if vm.Strategy == configschema.VirtualModelStrategyPriority {
		if preferredTarget, ok := r.preferred.Get(virtualID, capability); ok && preferredTarget != "" {
			orderedVM = reorderVMTargets(vm, preferredTarget)
		}
	}
	snapshot := r.buildAvailabilitySnapshot(ctx)
	decision, err := SelectPriorityTargetExcluding(virtualID, orderedVM, capability, snapshot, exclude)
	if err != nil {
		var noTarget *NoActiveTargetError
		if errors.As(err, &noTarget) {
			r.metrics.RecordNoActiveTarget(noTarget)
		}
		return nil, err
	}
	bare, providerID, _ := StripModelPrefix(strings.TrimSpace(decision.SelectedTarget))
	registered, getErr := r.registry.Get(providerID)
	if getErr != nil {
		// Availability said routable but the provider vanished between
		// snapshot construction and resolution; treat as no active target and
		// record the selected target so diagnostics show what disappeared.
		skipped := append(decision.Skipped, SkippedTarget{
			Target: decision.SelectedTarget,
			Index:  decision.SelectedIndex,
			Reason: availability.ReasonProviderNotRegistered,
		})
		noTarget := &NoActiveTargetError{VirtualModel: virtualID, Skipped: skipped}
		r.metrics.RecordNoActiveTarget(noTarget)
		return nil, noTarget
	}
	r.metrics.RecordSelection(decision)
	log.Printf("[router] umbrella %q → target=%q provider=%s upstream=%q (index=%d generation=%d)",
		virtualID, decision.SelectedTarget, registered.ID(), bare, decision.SelectedIndex, decision.Generation)
	return &Result{Provider: registered, ResolvedModel: bare, Selection: decision}, nil
}

// reorderVMTargets returns a copy of vm with preferredTarget moved to index 0
// and all other targets following in their original order (without duplication).
func reorderVMTargets(vm configschema.VirtualModel, preferredTarget string) configschema.VirtualModel {
	found := false
	for _, t := range vm.Targets {
		if strings.TrimSpace(t) == strings.TrimSpace(preferredTarget) {
			found = true
			break
		}
	}
	if !found {
		return vm
	}
	ordered := make([]string, 0, len(vm.Targets))
	ordered = append(ordered, preferredTarget)
	for _, t := range vm.Targets {
		if strings.TrimSpace(t) != strings.TrimSpace(preferredTarget) {
			ordered = append(ordered, t)
		}
	}
	return configschema.VirtualModel{Strategy: vm.Strategy, Targets: ordered}
}

// ResolveNext resolves the next failover target for an umbrella request,
// excluding all targets already attempted. It is called only from the
// sequential failover loop in processProxyRequest and must not be called for
// non-umbrella (explicit provider-prefixed) routes.
func (r *Router) ResolveNext(ctx context.Context, virtualID string, capability provider.Capability, exclude map[string]struct{}) (*Result, error) {
	vm, ok := r.routing.VirtualModels[virtualID]
	if !ok {
		return nil, &NoActiveTargetError{VirtualModel: virtualID}
	}
	return r.resolveVirtualModelExcluding(ctx, virtualID, vm, capability, exclude)
}

// RecordPreferred records target as the preferred starting candidate only for
// the legacy priority strategy. quota_priority always returns to configured
// order after cooldown expiry.
func (r *Router) RecordPreferred(virtualID string, capability provider.Capability, target string) {
	if r == nil || r.preferred == nil {
		return
	}
	vm, ok := r.routing.VirtualModels[virtualID]
	if !ok || vm.Strategy != configschema.VirtualModelStrategyPriority {
		return
	}
	r.preferred.Set(virtualID, capability, target)
}

// buildAvailabilitySnapshot constructs an availability view from the frozen
// registry without performing network I/O. Providers that expose a
// last-known-good catalog and allowlist contribute catalog state; others are
// treated as having no catalog (their targets will be skipped as
// model_not_in_catalog).
func (r *Router) buildAvailabilitySnapshot(ctx context.Context) *availability.Snapshot {
	registered := r.registry.All()
	views := make([]availability.ProviderView, 0, len(registered))
	for _, p := range registered {
		view := availability.ProviderView{
			ID:           p.ID(),
			Ready:        p.Health(ctx).Authenticated,
			Capabilities: append([]provider.Capability(nil), p.Capabilities()...),
		}
		if reporter, ok := p.(allowlistReporter); ok {
			view.AllowedModels = reporter.AllowedModelIDs()
		}
		cooldowns, expired := r.cooldowns.Cooldowns(p.ID())
		view.Cooldowns = cooldowns
		for _, cooldown := range expired {
			r.metrics.RecordCooldownExit(AddModelPrefix(p.ID(), cooldown.Model), "expired")
		}
		if catalog, ok := p.(lastKnownCatalog); ok {
			if models, fetchedAt, stale, present := catalog.LastKnownModels(); present {
				view.CatalogPresent = true
				view.CatalogModels = models
				view.CatalogFetchedAt = fetchedAt
				view.CatalogStale = stale
			}
		}
		views = append(views, view)
	}
	return availability.NewSnapshot(r.generation, nowFunc(), views)
}

func (r *Router) discoverFromCatalogs(ctx context.Context, model string, capability provider.Capability) []Result {
	var found []Result
	for _, registered := range r.registry.All() {
		if !hasCapability(registered, capability) {
			continue
		}
		models, err := registered.ListModels(ctx)
		if err != nil || models == nil {
			continue
		}
		for _, candidate := range models.Data {
			if strings.EqualFold(candidate.ID, model) {
				found = append(found, Result{Provider: registered, ResolvedModel: model})
				break
			}
		}
	}
	return found
}

func (r *Router) defaultProviderRoute(model string, capability provider.Capability) (*Result, error) {
	providerID := provider.ID(r.routing.DefaultProvider)
	if providerID == "" {
		providerID = provider.Copilot
	}
	registered, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found and default provider %q is unavailable", model, providerID)
	}
	if !hasCapability(registered, capability) {
		return nil, fmt.Errorf("default provider %q does not support capability %q", providerID, capability)
	}
	return &Result{Provider: registered, ResolvedModel: model}, nil
}

func hasCapability(registered provider.Provider, capability provider.Capability) bool {
	for _, supported := range registered.Capabilities() {
		if supported == capability {
			return true
		}
	}
	return false
}
