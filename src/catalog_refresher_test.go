package yarouter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartCatalogRefresher_RefreshesImmediatelyAndStopsCleanly(t *testing.T) {
	var calls atomic.Int32
	p := &mockProvider{id: ProviderCopilot, models: &ModelList{Object: "list", Data: []Model{{ID: "m1"}}}}
	countingList := func(ctx context.Context) (*ModelList, error) {
		calls.Add(1)
		return p.models, nil
	}
	wrapped := &listFuncProvider{mockProvider: p, listFn: countingList}

	stop := startCatalogRefresher(context.Background(), func() []Provider { return []Provider{wrapped} })
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("expected at least one immediate refresh call")
	}
	stop() // must return promptly and leave no goroutine running
}

func TestStartCatalogRefresher_StopWaitsForLoopExit(t *testing.T) {
	p := &mockProvider{id: ProviderCopilot, models: &ModelList{Object: "list"}}
	stop := startCatalogRefresher(context.Background(), func() []Provider { return []Provider{p} })
	// Calling stop must not race with or leak the ticker goroutine; run under
	// -race to catch it.
	stop()
}

func TestStartCatalogRefresher_FailedRefreshKeepsLastKnownGood(t *testing.T) {
	cache := NewModelCache(10 * time.Millisecond)
	cache.Set(&ModelList{Object: "list", Data: []Model{{ID: "m1"}}})

	var fail atomic.Bool
	fail.Store(true)
	p := &mockProvider{id: ProviderCopilot}
	wrapped := &listFuncProvider{mockProvider: p, listFn: func(ctx context.Context) (*ModelList, error) {
		if fail.Load() {
			return cache.GetOrFetch(func() (*ModelList, error) { return nil, errBoom })
		}
		return cache.GetOrFetch(func() (*ModelList, error) { return &ModelList{Object: "list", Data: []Model{{ID: "m1"}}}, nil })
	}}

	// Simulate: cache was populated, then goes stale, then a refresh attempt
	// fails. Last-known-good must remain readable and non-empty throughout —
	// reproducing the HTTP200 -> TTL boundary -> 503 sequence from issue #93
	// and proving it no longer drops routing data.
	time.Sleep(20 * time.Millisecond) // cross the cache TTL
	refreshAllCatalogs(context.Background(), []Provider{wrapped})

	models, _, stale, ok := cache.LastKnownGood()
	if !ok || models == nil || len(models.Data) != 1 {
		t.Fatalf("last-known-good catalog lost after failed refresh: ok=%v models=%v", ok, models)
	}
	if !stale {
		t.Fatal("expected catalog to be marked stale, not discarded")
	}
}

var errBoom = &staticError{"boom"}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }

// listFuncProvider lets a test override ListModels while reusing mockProvider
// for the rest of the Provider interface.
type listFuncProvider struct {
	*mockProvider
	listFn func(ctx context.Context) (*ModelList, error)
}

func (p *listFuncProvider) ListModels(ctx context.Context) (*ModelList, error) {
	return p.listFn(ctx)
}

func TestStartCatalogRefresher_PicksUpActiveProviderChanges(t *testing.T) {
	var mu sync.Mutex
	var providers []Provider
	activeProviders := func() []Provider {
		mu.Lock()
		defer mu.Unlock()
		return append([]Provider(nil), providers...)
	}

	var calls atomic.Int32
	p := &listFuncProvider{
		mockProvider: &mockProvider{id: ProviderCodex},
		listFn: func(ctx context.Context) (*ModelList, error) {
			calls.Add(1)
			return &ModelList{Object: "list"}, nil
		},
	}

	originalInterval := catalogRefreshInterval
	catalogRefreshInterval = 5 * time.Millisecond
	t.Cleanup(func() { catalogRefreshInterval = originalInterval })

	stop := startCatalogRefresher(context.Background(), activeProviders)
	defer stop()

	if calls.Load() != 0 {
		t.Fatalf("expected no providers refreshed before any were added, got %d calls", calls.Load())
	}

	mu.Lock()
	providers = []Provider{p}
	mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("expected refresh loop to pick up newly active provider on next tick")
	}
}
