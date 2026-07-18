// catalog_refresher.go proactively refreshes provider model catalogs in the
// background. Umbrella-model routing evaluates a network-free last-known-good
// catalog view (see provider_availability.go); without a request that
// triggers ListModels, that view is never populated or kept current. This
// loop reuses each provider's existing single-flight ModelCache so thiendu
// routing does not depend on client traffic to stay fresh across the catalog
// TTL boundary (see issue #93).
package yarouter

import (
	"context"
	"log"
	"sync"
	"time"
)

// catalogRefreshInterval refreshes at half the shared model-cache TTL so a
// single missed/failed refresh cannot let a catalog age past the TTL before
// the next attempt. It is a var so tests can shorten it; production always
// uses the default.
var catalogRefreshInterval = defaultModelCacheTTL / 2

// startCatalogRefresher launches one background refresh loop that calls
// ListModels for every currently active provider, immediately and then on
// catalogRefreshInterval. activeProviders is invoked on each tick so
// enable/disable changes are picked up without restarting the loop. The
// returned stop function cancels the loop and blocks until its goroutine has
// exited, so shutdown never leaves it running.
func startCatalogRefresher(ctx context.Context, activeProviders func() []Provider) (stop func()) {
	loopCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		refreshAllCatalogs(loopCtx, activeProviders())
		ticker := time.NewTicker(catalogRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				refreshAllCatalogs(loopCtx, activeProviders())
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

// refreshAllCatalogs fetches each provider's catalog through its existing
// coalesced ListModels path. A failed fetch is logged and otherwise ignored:
// ModelCache only overwrites its last-known-good entry on success, so a
// failure here never discards previously cached routing data.
func refreshAllCatalogs(ctx context.Context, providers []Provider) {
	for _, p := range providers {
		if _, err := p.ListModels(ctx); err != nil {
			log.Printf("[catalog-refresh] provider=%s refresh failed, keeping last-known-good catalog: %v", p.ID(), err)
		}
	}
}
