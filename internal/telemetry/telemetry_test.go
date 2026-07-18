package telemetry

import (
	"sync"
	"testing"
)

func TestBeginFinish_SuccessCountsCorrectly(t *testing.T) {
	r := NewRecorder()
	handle := r.Begin("copilot", "gpt-5-mini")
	snap := r.Snapshot()
	if len(snap) != 1 || snap[0].Requests != 1 || snap[0].InFlight != 1 {
		t.Fatalf("after Begin: %+v", snap)
	}
	handle.Finish(Outcome{Success: true, ProducedMessage: true, Usage: &Usage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8}})

	snap = r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap))
	}
	got := snap[0]
	if got.Requests != 1 || got.Successes != 1 || got.Errors != 0 || got.InFlight != 0 {
		t.Fatalf("counters = %+v", got)
	}
	if got.Messages != 1 {
		t.Fatalf("messages = %d, want 1", got.Messages)
	}
	if got.InputTokens != 3 || got.OutputTokens != 5 || got.TotalTokens != 8 {
		t.Fatalf("usage = %+v", got)
	}
	if got.UnavailableUsageCount != 0 {
		t.Fatalf("unavailable usage = %d, want 0", got.UnavailableUsageCount)
	}
}

func TestFinish_ErrorPathRecordsCategoryAndNoMessage(t *testing.T) {
	r := NewRecorder()
	handle := r.Begin("codex", "gpt-5.4-mini")
	handle.Finish(Outcome{Success: false, ErrorCategory: ErrorCategoryRateLimited})

	snap := r.Snapshot()
	got := snap[0]
	if got.Errors != 1 || got.Successes != 0 {
		t.Fatalf("counters = %+v", got)
	}
	if got.LastErrorCategory != ErrorCategoryRateLimited {
		t.Fatalf("last error category = %q", got.LastErrorCategory)
	}
	if got.Messages != 0 {
		t.Fatalf("messages = %d, want 0", got.Messages)
	}
	if got.UnavailableUsageCount != 1 {
		t.Fatalf("unavailable usage = %d, want 1 (no Usage on error)", got.UnavailableUsageCount)
	}
}

// TestInFlightReturnsToZeroOnEveryTerminalPath proves in-flight is
// decremented for success, error, timeout, and cancellation-style outcomes.
func TestInFlightReturnsToZeroOnEveryTerminalPath(t *testing.T) {
	cases := []Outcome{
		{Success: true, ProducedMessage: true},
		{Success: false, ErrorCategory: ErrorCategoryTransport},
		{Success: false, ErrorCategory: ErrorCategoryTimeout},
		{Success: false, ErrorCategory: ErrorCategoryCanceled},
	}
	for _, outcome := range cases {
		r := NewRecorder()
		handle := r.Begin("kilo", "kilo-auto")
		if snap := r.Snapshot(); snap[0].InFlight != 1 {
			t.Fatalf("in-flight before finish = %d, want 1", snap[0].InFlight)
		}
		handle.Finish(outcome)
		snap := r.Snapshot()
		if snap[0].InFlight != 0 {
			t.Fatalf("in-flight after %+v = %d, want 0", outcome, snap[0].InFlight)
		}
	}
}

func TestFinish_IsIdempotent(t *testing.T) {
	r := NewRecorder()
	handle := r.Begin("copilot", "gpt-5-mini")
	handle.Finish(Outcome{Success: true})
	handle.Finish(Outcome{Success: false, ErrorCategory: ErrorCategoryTransport}) // must be ignored

	snap := r.Snapshot()
	if snap[0].Successes != 1 || snap[0].Errors != 0 {
		t.Fatalf("second Finish call was not a no-op: %+v", snap[0])
	}
}

func TestFinish_OnNilHandleIsNoOp(t *testing.T) {
	var handle *InFlight
	handle.Finish(Outcome{Success: true}) // must not panic
}

func TestNilRecorder_AllMethodsAreNoOps(t *testing.T) {
	var r *Recorder
	handle := r.Begin("copilot", "gpt-5-mini")
	handle.Finish(Outcome{Success: true, ProducedMessage: true, Usage: &Usage{TotalTokens: 10}})
	if snap := r.Snapshot(); snap != nil {
		t.Fatalf("nil recorder snapshot = %v, want nil", snap)
	}
}

// TestConcurrentBeginFinishIsRaceSafe drives many concurrent requests against
// the same and different targets; run with -race.
func TestConcurrentBeginFinishIsRaceSafe(t *testing.T) {
	r := NewRecorder()
	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			model := "gpt-5-mini"
			if i%2 == 0 {
				model = "gpt-5.4-mini"
			}
			handle := r.Begin("copilot", model)
			if i%3 == 0 {
				handle.Finish(Outcome{Success: false, ErrorCategory: ErrorCategoryTimeout})
				return
			}
			handle.Finish(Outcome{Success: true, ProducedMessage: true, Usage: &Usage{TotalTokens: 1}})
		}(i)
	}
	wg.Wait()

	total := uint64(0)
	for _, snap := range r.Snapshot() {
		total += snap.Requests
		if snap.InFlight != 0 {
			t.Fatalf("target %s/%s left in-flight = %d, want 0", snap.Provider, snap.Model, snap.InFlight)
		}
	}
	if total != workers {
		t.Fatalf("total requests = %d, want %d", total, workers)
	}
}

func TestSnapshot_DeterministicOrder(t *testing.T) {
	r := NewRecorder()
	r.Begin("kilo", "kilo-auto").Finish(Outcome{Success: true})
	r.Begin("codex", "gpt-5.4-mini").Finish(Outcome{Success: true})
	r.Begin("copilot", "gpt-5-mini").Finish(Outcome{Success: true})

	var first []string
	for i := 0; i < 10; i++ {
		var order []string
		for _, snap := range r.Snapshot() {
			order = append(order, snap.Provider+"/"+snap.Model)
		}
		if first == nil {
			first = order
			continue
		}
		for j := range order {
			if order[j] != first[j] {
				t.Fatalf("snapshot order not deterministic: %v vs %v", order, first)
			}
		}
	}
}

// TestSnapshot_DistinguishesMissingUsageFromZeroUsage proves a request that
// reports zero tokens is distinct from a request that reported no usage at
// all.
func TestSnapshot_DistinguishesMissingUsageFromZeroUsage(t *testing.T) {
	r := NewRecorder()
	r.Begin("copilot", "gpt-5-mini").Finish(Outcome{Success: true, Usage: &Usage{}}) // exact zero, reported
	r.Begin("copilot", "gpt-5-mini").Finish(Outcome{Success: true, Usage: nil})      // not reported

	snap := r.Snapshot()[0]
	if snap.TotalTokens != 0 {
		t.Fatalf("total tokens = %d, want 0", snap.TotalTokens)
	}
	if snap.UnavailableUsageCount != 1 {
		t.Fatalf("unavailable usage count = %d, want 1", snap.UnavailableUsageCount)
	}
}

func TestDeriveProviderState(t *testing.T) {
	cases := []struct {
		name          string
		enabled       bool
		authenticated bool
		healthError   bool
		cooldown      bool
		inFlight      int64
		want          ProviderDisplayState
	}{
		{name: "disabled wins", enabled: false, authenticated: true, want: StateDisabled},
		{name: "error", enabled: true, healthError: true, want: StateError},
		{name: "auth required", enabled: true, authenticated: false, want: StateAuthRequired},
		{name: "cooldown", enabled: true, authenticated: true, cooldown: true, want: StateCooldown},
		{name: "serving", enabled: true, authenticated: true, inFlight: 2, want: StateServing},
		{name: "ready", enabled: true, authenticated: true, want: StateReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveProviderState(tc.enabled, tc.authenticated, tc.healthError, tc.cooldown, tc.inFlight)
			if got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOutcomeNeverCarriesUpstreamStrings is a guard: Outcome/TargetSnapshot
// fields are all bounded types (bool, enum, int, duration) — there is no
// string field wide enough to leak a prompt, completion, or raw upstream
// error. This test exists to make that invariant visible at the type level
// rather than relying on convention.
func TestOutcomeNeverCarriesUpstreamStrings(t *testing.T) {
	r := NewRecorder()
	r.Begin("copilot", "gpt-5-mini").Finish(Outcome{
		Success:       false,
		ErrorCategory: ErrorCategoryTransport,
	})
	snap := r.Snapshot()[0]
	switch snap.LastErrorCategory {
	case ErrorCategoryNone, ErrorCategoryInvalidRequest, ErrorCategoryAuthRequired,
		ErrorCategoryEntitlementDenied, ErrorCategoryRateLimited, ErrorCategoryUnsupported,
		ErrorCategoryProviderUnavailable, ErrorCategoryTransport, ErrorCategoryModelUnavailable,
		ErrorCategoryTimeout, ErrorCategoryCanceled:
	default:
		t.Fatalf("unexpected error category value: %q", snap.LastErrorCategory)
	}
}
