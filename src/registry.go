// registry.go — ProviderRegistry holds and dispatches to configured providers.
package main

import (
	"fmt"
	"sync"
)

// ProviderRegistry holds providers in deterministic registration order.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
	order     []ProviderID
}

func NewProviderRegistry() *ProviderRegistry {
	registry := &ProviderRegistry{providers: make(map[ProviderID]Provider)}
	setHealthRegistry(registry)
	return registry
}

func (r *ProviderRegistry) Register(provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[provider.ID()]; !exists {
		r.order = append(r.order, provider.ID())
	}
	r.providers[provider.ID()] = provider
}

func (r *ProviderRegistry) Get(id ProviderID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", id)
	}
	return provider, nil
}

func (r *ProviderRegistry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		providers = append(providers, r.providers[id])
	}
	return providers
}
