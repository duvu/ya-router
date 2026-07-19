// failover_test.go — tests for sequential umbrella failover (issues #96–#99).
package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	controlpkg "github.com/duvu/ya-router/internal/control"
	routingpkg "github.com/duvu/ya-router/internal/routing"
)

// buildTwoTargetHandler creates a proxy handler whose umbrella model "thiendu"
// has two targets: copilot (returns firstStatus) and codex (returns secondStatus).
// It returns the handler and pointers to per-target call counters.
func buildTwoTargetHandler(t *testing.T, firstStatus, secondStatus int) (http.Handler, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var callsA, callsB atomic.Int32
	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.WriteHeader(firstStatus)
			return nil
		},
	}
	codex := &mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(secondStatus)
			return nil
		},
	}
	registry := NewProviderRegistry()
	registry.Register(copilot)
	registry.Register(codex)
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	router := NewModelRouter(registry, cfg.Routing)
	return proxyHandler(registry, router, cfg), &callsA, &callsB
}

// TestFailoverOn429FirstTargetSuccessSecond proves a 429 on the first target
// causes the same request to be retried on the second target which succeeds.
func TestFailoverOn429FirstTargetSuccessSecond(t *testing.T) {
	handler, callsA, callsB := buildTwoTargetHandler(t, http.StatusTooManyRequests, http.StatusOK)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want 1", callsA.Load())
	}
	if callsB.Load() != 1 {
		t.Fatalf("target B calls = %d, want 1", callsB.Load())
	}
}

// TestFailoverOn5xxFirstTargetSuccessSecond proves a 500 on the first target
// causes failover to the second.
func TestFailoverOn5xxFirstTargetSuccessSecond(t *testing.T) {
	handler, callsA, callsB := buildTwoTargetHandler(t, http.StatusInternalServerError, http.StatusOK)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want 1", callsA.Load())
	}
	if callsB.Load() != 1 {
		t.Fatalf("target B calls = %d, want 1", callsB.Load())
	}
}

// TestFailoverAllTargetsFail proves all targets failing returns one sanitized
// 503 and both targets are attempted exactly once.
func TestFailoverAllTargetsFail(t *testing.T) {
	handler, callsA, callsB := buildTwoTargetHandler(t, http.StatusTooManyRequests, http.StatusInternalServerError)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Type != string(ProviderErrorModelUnavailable) {
		t.Fatalf("error type = %q, want model_unavailable", payload.Error.Type)
	}
	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want 1", callsA.Load())
	}
	if callsB.Load() != 1 {
		t.Fatalf("target B calls = %d, want 1", callsB.Load())
	}
}

// TestFailoverChainABSucceedC proves A and B both fail, then C succeeds.
func TestFailoverChainABSucceedC(t *testing.T) {
	var callsA, callsB, callsC atomic.Int32
	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	}
	codex := &mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		},
	}
	kilo := &mockProvider{
		id: ProviderKilo, name: "Kilo", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"kilo-auto"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsC.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	registry := NewProviderRegistry()
	registry.Register(copilot)
	registry.Register(codex)
	registry.Register(kilo)
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini", "kilo/kilo-auto"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if callsA.Load() != 1 || callsB.Load() != 1 || callsC.Load() != 1 {
		t.Fatalf("calls A=%d B=%d C=%d, want all 1", callsA.Load(), callsB.Load(), callsC.Load())
	}
}

// TestFailoverNoTargetAttemptedTwice proves a target that fails is not
// retried within the same logical request.
func TestFailoverNoTargetAttemptedTwice(t *testing.T) {
	var callsA, callsB atomic.Int32
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want exactly 1 (no re-dispatch)", callsA.Load())
	}
}

// TestFailoverInvalidRequestNoFailover proves HTTP 400 (invalid request)
// does not trigger failover — only one target is attempted.
func TestFailoverInvalidRequestNoFailover(t *testing.T) {
	var callsA, callsB atomic.Int32
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want 1", callsA.Load())
	}
	if callsB.Load() != 0 {
		t.Fatalf("target B calls = %d, want 0 (no failover on 400)", callsB.Load())
	}
}

// TestFailoverCancelledContextNoFurtherDispatch proves that after a context
// cancellation, no further targets are dispatched.
func TestFailoverCancelledContextNoFurtherDispatch(t *testing.T) {
	var callsB atomic.Int32
	registry := NewProviderRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			cancel() // cancel context during the first attempt
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	router := NewModelRouter(registry, cfg.Routing)
	rw := &responseWrapper{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`))

	_ = processProxyRequest(registry, router, cfg, rw, r, ctx)

	if callsB.Load() != 0 {
		t.Fatalf("target B calls = %d after cancellation, want 0", callsB.Load())
	}
}

// TestFailoverExplicitProviderPrefixNeverFailsOver proves explicit
// provider-prefixed requests dispatch exactly once with no failover.
func TestFailoverExplicitProviderPrefixNeverFailsOver(t *testing.T) {
	var callsA, callsB atomic.Int32
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		models:    &ModelList{Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"github/gpt-5-mini","messages":[]}`)))

	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want exactly 1", callsA.Load())
	}
	if callsB.Load() != 0 {
		t.Fatalf("target B calls = %d, want 0 (no failover for explicit routes)", callsB.Load())
	}
}

// TestFailoverAfterStreamingOutputNoSecondProvider proves a failure AFTER
// streaming output has begun does not dispatch another provider.
func TestFailoverAfterStreamingOutputNoSecondProvider(t *testing.T) {
	var callsA, callsB atomic.Int32
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsA.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {\"model\":\"gpt-5-mini\"}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			callsB.Add(1)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[],"stream":true}`)))

	if callsA.Load() != 1 {
		t.Fatalf("target A calls = %d, want 1", callsA.Load())
	}
	if callsB.Load() != 0 {
		t.Fatalf("target B calls = %d, want 0 (output committed, no failover)", callsB.Load())
	}
}

// TestPreferredTargetAfterSuccess proves that after target B succeeds following
// a failover from A, the next request for the same virtual model attempts B first.
func TestPreferredTargetAfterSuccess(t *testing.T) {
	var callsA1, callsB1 atomic.Int32
	var callsA2, callsB2 atomic.Int32

	var copilotFunc, codexFunc atomic.Value // stores func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error

	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error {
			if fn := copilotFunc.Load(); fn != nil {
				return fn.(func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error)(ctx, w, r, body, cap)
			}
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	codex := &mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error {
			if fn := codexFunc.Load(); fn != nil {
				return fn.(func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error)(ctx, w, r, body, cap)
			}
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}

	registry := NewProviderRegistry()
	registry.Register(copilot)
	registry.Register(codex)
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	// Shared router preserves preferred-target state across both requests.
	router := NewModelRouter(registry, cfg.Routing)
	handler := proxyHandler(registry, router, cfg)

	// Phase 1: copilot → 429, codex → 200. Codex becomes preferred.
	type proxyFn = func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error
	copilotFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsA1.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		return nil
	}))
	codexFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsB1.Add(1)
		w.WriteHeader(http.StatusOK)
		return nil
	}))

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1 status = %d, want 200", rec1.Code)
	}

	// Phase 2: both succeed. Codex (preferred) should be tried first.
	copilotFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsA2.Add(1)
		w.WriteHeader(http.StatusOK)
		return nil
	}))
	codexFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsB2.Add(1)
		w.WriteHeader(http.StatusOK)
		return nil
	}))

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("request 2 status = %d, want 200", rec2.Code)
	}

	// Request 2 must prefer codex and return on first try without calling copilot.
	if callsB2.Load() != 1 {
		t.Fatalf("request 2 codex (preferred) calls = %d, want 1", callsB2.Load())
	}
	if callsA2.Load() != 0 {
		t.Fatalf("request 2 copilot calls = %d, want 0 (codex is preferred)", callsA2.Load())
	}
}

// TestPreferredTargetFallsBackWhenPreferredFails proves that when the preferred
// target fails on a later request, the failover loop continues to the next
// available target. Phase 1 establishes codex as preferred by having copilot
// fail and codex succeed. Phase 2 has codex fail but copilot succeed; because
// Phase 2 uses a different router there are no lingering cooldowns from Phase 1.
func TestPreferredTargetFallsBackWhenPreferredFails(t *testing.T) {
	type proxyFn = func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error

	var copilotFunc, codexFunc atomic.Value

	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error {
			if fn := copilotFunc.Load(); fn != nil {
				return fn.(proxyFn)(ctx, w, r, body, cap)
			}
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	codex := &mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error {
			if fn := codexFunc.Load(); fn != nil {
				return fn.(proxyFn)(ctx, w, r, body, cap)
			}
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}

	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}

	// Phase 1: A (copilot) → 429, B (codex) → OK. B becomes preferred.
	// Use a fresh registry so cooldowns don't carry over.
	reg1 := NewProviderRegistry()
	reg1.Register(copilot)
	reg1.Register(codex)
	router1 := NewModelRouter(reg1, cfg.Routing)
	handler1 := proxyHandler(reg1, router1, cfg)

	var callsA1, callsB1 atomic.Int32
	copilotFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsA1.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		return nil
	}))
	codexFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsB1.Add(1)
		w.WriteHeader(http.StatusOK)
		return nil
	}))
	rec1 := httptest.NewRecorder()
	handler1.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("phase 1 status = %d", rec1.Code)
	}
	// Codex is now preferred for thiendu/chat.

	// Phase 2: fresh registry (no cooldowns) but same preferred-target state.
	// B (codex, preferred) → 429, A (copilot) → OK.
	reg2 := NewProviderRegistry()
	reg2.Register(copilot)
	reg2.Register(codex)
	// Build a new router that shares the preferred-target registry from router1.
	// We accomplish this by noting that after phase 1 the preference is stored
	// in the router's preferred registry. To test fallback we use the same router.
	// Because copilot was on cooldown in router1 from phase 1's 429, we need to
	// observe that the preferred target (codex) is tried first and then falls back.
	// Use handler1/router1 (same preferred + cooldown state).

	var callsA2, callsB2 atomic.Int32
	copilotFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsA2.Add(1)
		w.WriteHeader(http.StatusOK)
		return nil
	}))
	codexFunc.Store(proxyFn(func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
		callsB2.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		return nil
	}))

	// Build a second handler sharing the same preferred registry as handler1 but
	// with a new cooldown registry so copilot's phase-1 cooldown is cleared.
	reg2b := NewProviderRegistry()
	reg2b.Register(copilot)
	reg2b.Register(codex)
	// Access preferred registry via helper — we use the routing package directly.
	prefReg := routingpkg.NewPreferredTargetRegistry()
	prefReg.Set("thiendu", "chat", "codex/gpt-5.4-mini") // set B as preferred manually
	router2 := routingpkg.NewRouterWithRegistries(reg2b, cfg.Routing, routingpkg.NewCooldownRegistry(), prefReg)
	handler2 := proxyHandler(reg2b, router2, cfg)

	rec2 := httptest.NewRecorder()
	handler2.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("phase 2 status = %d", rec2.Code)
	}
	// B (codex, preferred) was tried first and failed; A (copilot) succeeded.
	if callsB2.Load() != 1 {
		t.Fatalf("phase 2 codex (preferred, failing) calls = %d, want 1", callsB2.Load())
	}
	if callsA2.Load() != 1 {
		t.Fatalf("phase 2 copilot (fallback, succeeding) calls = %d, want 1", callsA2.Load())
	}
}

// TestFailoverModelBodyRewrittenOnEachAttempt proves the request body model
// is correctly rewritten to the upstream model for each attempt.
func TestFailoverModelBodyRewrittenOnEachAttempt(t *testing.T) {
	var bodyA, bodyB string
	var mu sync.Mutex

	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			mu.Lock()
			bodyA = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			mu.Lock()
			bodyB = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"thiendu","messages":[]}`)))

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(bodyA, `"gpt-5-mini"`) {
		t.Fatalf("attempt 1 body not rewritten to gpt-5-mini: %s", bodyA)
	}
	if strings.Contains(bodyA, "thiendu") {
		t.Fatalf("attempt 1 body still has thiendu: %s", bodyA)
	}
	if !strings.Contains(bodyB, `"gpt-5.4-mini"`) {
		t.Fatalf("attempt 2 body not rewritten to gpt-5.4-mini: %s", bodyB)
	}
}

// TestPreferredTargetRegistryConcurrentSafety proves concurrent reads and
// writes to the registry do not race (run with -race to detect).
func TestPreferredTargetRegistryConcurrentSafety(t *testing.T) {
	reg := routingpkg.NewPreferredTargetRegistry()
	var wg sync.WaitGroup
	const goroutines = 50
	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			reg.Set("thiendu", "chat", "codex/gpt-5.4-mini")
		}()
		go func() {
			defer wg.Done()
			reg.Get("thiendu", "chat")
		}()
	}
	wg.Wait()
}

// TestDashboardWSStatePreservedOnRESTReload reproduces the
// "No live provider state yet" overwrite bug: a periodic REST reload must not
// zero out the live wsState that the WS connection already pushed.
func TestDashboardWSStatePreservedOnRESTReload(t *testing.T) {
	m := newDashboardModel(clientCommonFlags{}, nil)

	// Simulate a WS snapshot arriving with non-zero state.
	m.snapshot.wsState = controlpkg.WSStatePayload{
		Providers: []controlpkg.ProviderStateView{{Provider: "copilot", State: "authenticated"}},
	}

	// Simulate a REST reload arriving (wsState is zero in the incoming snapshot).
	restMsg := dashboardLoadedMsg{snapshot: dashboardSnapshot{}}
	next, _ := m.Update(restMsg)
	updated := next.(dashboardModel)

	// The WS state must be preserved after the REST reload.
	if len(updated.snapshot.wsState.Providers) == 0 {
		t.Fatal("wsState was erased by REST reload; expected live provider state to be preserved")
	}
}

// TestChatModelOnRouteFalloverDoesNotOpenNewBubble proves that a second
// chat.route event (failover) updates in-place rather than adding a new
// assistant transcript entry.
func TestChatModelOnRouteFalloverDoesNotOpenNewBubble(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage()
	_ = m.composer // already cleared

	// First chat.route — opens the assistant bubble.
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	if m.state != chatStreaming {
		t.Fatalf("state = %v, want chatStreaming after first chat.route", m.state)
	}
	entriesAfterFirstRoute := len(m.transcript)
	if entriesAfterFirstRoute < 1 {
		t.Fatal("expected at least one transcript entry after first chat.route")
	}

	// Second chat.route (failover) — must NOT add another assistant entry.
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "codex", ResolvedModel: "gpt-5.4-mini"})
	if len(m.transcript) != entriesAfterFirstRoute {
		t.Fatalf("transcript grew from %d to %d entries on failover chat.route; expected no new bubble",
			entriesAfterFirstRoute, len(m.transcript))
	}
	if m.selectedProvider != "codex" {
		t.Fatalf("selectedProvider = %q, want codex after failover chat.route", m.selectedProvider)
	}
}
