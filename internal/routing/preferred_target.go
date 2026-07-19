package routing

import (
	"sync"

	"github.com/duvu/ya-router/internal/provider"
)

// preferredTargetKey identifies one virtual-model/capability slot.
type preferredTargetKey struct {
	virtualModel string
	capability   provider.Capability
}

// PreferredTargetRegistry stores the last successful umbrella target per
// virtual-model/capability in memory. It is shared across immutable runtime
// snapshots in the same way CooldownRegistry is shared, so a successful target
// carries forward across config reloads. It resets on daemon restart.
type PreferredTargetRegistry struct {
	mu      sync.RWMutex
	entries map[preferredTargetKey]string // key → full target string (e.g. "kilo/kilo-auto/free")
}

// NewPreferredTargetRegistry returns an empty, ready-to-use registry.
func NewPreferredTargetRegistry() *PreferredTargetRegistry {
	return &PreferredTargetRegistry{entries: make(map[preferredTargetKey]string)}
}

// Set records target as the preferred starting point for the next request
// that resolves the same virtual model and capability.
func (r *PreferredTargetRegistry) Set(virtualModel string, capability provider.Capability, target string) {
	if r == nil || virtualModel == "" || capability == "" || target == "" {
		return
	}
	r.mu.Lock()
	r.entries[preferredTargetKey{virtualModel, capability}] = target
	r.mu.Unlock()
}

// Get returns the stored preferred target for the given virtual model and
// capability, or ("", false) when no preference is recorded.
func (r *PreferredTargetRegistry) Get(virtualModel string, capability provider.Capability) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	t, ok := r.entries[preferredTargetKey{virtualModel, capability}]
	r.mu.RUnlock()
	return t, ok
}
