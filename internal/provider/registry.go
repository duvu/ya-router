package provider

import (
	"fmt"
	"sync"
)

// Registry holds providers in deterministic registration order.
// Construction is side-effect free so tests and future runtime snapshots can
// own isolated registries.
type Registry struct {
	mu        sync.RWMutex
	providers map[ID]Provider
	order     []ID
}

// NewRegistry returns an empty, isolated provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[ID]Provider)}
}

// Register adds or replaces a provider while retaining registration order.
func (r *Registry) Register(provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[provider.ID()]; !exists {
		r.order = append(r.order, provider.ID())
	}
	r.providers[provider.ID()] = provider
}

// Get returns a provider by ID.
func (r *Registry) Get(id ID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", id)
	}
	return registered, nil
}

// All returns a registration-ordered snapshot of the providers.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		providers = append(providers, r.providers[id])
	}
	return providers
}
