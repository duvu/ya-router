// Package telemetry maintains small, in-memory, redacted counters describing
// data-plane activity per canonical provider/model target. Counters mean
// "since daemon start" and are never persisted. Reads never trigger network
// I/O and recording never fails or blocks the request it describes: every
// method here is a pure in-memory update guarded by a mutex, so a bug in this
// package can slow a request but cannot change its outcome.
//
// This package never retains prompts, completions, credentials, account IDs,
// or raw upstream error strings. Only configured provider/model identifiers
// and the bounded ErrorCategory/ProviderDisplayState enums are stored.
package telemetry

import (
	"sort"
	"sync"
	"time"
)

// ErrorCategory is a stable, bounded classification of a terminal request
// failure. It never carries an upstream error message.
type ErrorCategory string

const (
	ErrorCategoryNone                ErrorCategory = ""
	ErrorCategoryInvalidRequest      ErrorCategory = "invalid_request"
	ErrorCategoryAuthRequired        ErrorCategory = "auth_required"
	ErrorCategoryEntitlementDenied   ErrorCategory = "entitlement_denied"
	ErrorCategoryRateLimited         ErrorCategory = "rate_limited"
	ErrorCategoryUnsupported         ErrorCategory = "unsupported_capability"
	ErrorCategoryProviderUnavailable ErrorCategory = "provider_unavailable"
	ErrorCategoryTransport           ErrorCategory = "transport_error"
	ErrorCategoryModelUnavailable    ErrorCategory = "model_unavailable"
	ErrorCategoryTimeout             ErrorCategory = "timeout"
	ErrorCategoryCanceled            ErrorCategory = "canceled"
)

// ProviderDisplayState is the small, stable set of provider states the TUI
// may show. It is derived from existing health/cooldown/config state, not
// tracked independently.
type ProviderDisplayState string

const (
	StateServing      ProviderDisplayState = "SERVING"
	StateReady        ProviderDisplayState = "READY"
	StateCooldown     ProviderDisplayState = "COOLDOWN"
	StateAuthRequired ProviderDisplayState = "AUTH_REQUIRED"
	StateDisabled     ProviderDisplayState = "DISABLED"
	StateError        ProviderDisplayState = "ERROR"
)

// Usage is exact upstream-reported token usage for one completed request. A
// nil *Usage passed to Finish means usage was not reported and is counted as
// unavailable, never estimated.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Outcome is the terminal result of one request, recorded exactly once via
// InFlight.Finish.
type Outcome struct {
	// Success is true for a 2xx completion; false for any other terminal
	// path (error, timeout, or cancellation).
	Success bool
	// ErrorCategory classifies a non-success outcome. Ignored when Success.
	ErrorCategory ErrorCategory
	// ProducedMessage is true when the request yielded one completed
	// assistant message (chat/responses), incrementing Messages by exactly
	// one. Requests that don't produce a message (e.g. embeddings, or a
	// terminal error before any content was produced) leave it false.
	ProducedMessage bool
	// Usage is the exact upstream-reported token usage, or nil when the
	// upstream response did not report it.
	Usage *Usage
}

// TargetSnapshot is a redacted, point-in-time read of one provider/model
// target's counters.
type TargetSnapshot struct {
	Provider              string
	Model                 string
	Requests              uint64
	Successes             uint64
	Errors                uint64
	InFlight              int64
	Messages              uint64
	LastLatency           time.Duration
	LastErrorCategory     ErrorCategory
	InputTokens           int64
	OutputTokens          int64
	TotalTokens           int64
	UnavailableUsageCount uint64
}

type targetState struct {
	requests              uint64
	successes             uint64
	errors                uint64
	inFlight              int64
	messages              uint64
	lastLatency           time.Duration
	lastErrorCategory     ErrorCategory
	inputTokens           int64
	outputTokens          int64
	totalTokens           int64
	unavailableUsageCount uint64
}

// Recorder is a concurrency-safe sink for per-target request/usage counters.
// The zero value is not usable; construct with NewRecorder. A nil *Recorder
// is safe to call methods on (they are no-ops), so callers need not branch on
// telemetry being wired up.
type Recorder struct {
	mu      sync.Mutex
	targets map[string]*targetState
}

// NewRecorder returns an empty telemetry sink.
func NewRecorder() *Recorder {
	return &Recorder{targets: make(map[string]*targetState)}
}

func targetKey(providerID, model string) string {
	return providerID + "\x00" + model
}

func splitTargetKey(key string) (provider, model string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

func (r *Recorder) stateLocked(providerID, model string) *targetState {
	key := targetKey(providerID, model)
	state, ok := r.targets[key]
	if !ok {
		state = &targetState{}
		r.targets[key] = state
	}
	return state
}

// InFlight tracks one in-progress request between Begin and Finish.
type InFlight struct {
	recorder   *Recorder
	providerID string
	model      string
	start      time.Time
	done       bool
	mu         sync.Mutex
}

// Begin records the start of one request against providerID/model: it
// increments Requests and InFlight immediately so InFlight is accurate even
// if the caller never reaches a terminal state (e.g. a panic recovers
// upstream). The returned handle's Finish must be called exactly once on
// every terminal path (success, error, timeout, cancellation) to return
// InFlight to zero; calling it more than once is a safe no-op after the
// first call.
func (r *Recorder) Begin(providerID, model string) *InFlight {
	handle := &InFlight{providerID: providerID, model: model, start: time.Now()}
	if r == nil {
		return handle
	}
	handle.recorder = r
	r.mu.Lock()
	state := r.stateLocked(providerID, model)
	state.requests++
	state.inFlight++
	r.mu.Unlock()
	return handle
}

// Finish records the terminal outcome of the request started by Begin. It is
// safe to call on a nil handle and safe to call more than once (only the
// first call is recorded), so callers may use defer unconditionally.
func (h *InFlight) Finish(outcome Outcome) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return
	}
	h.done = true
	h.mu.Unlock()
	if h.recorder == nil {
		return
	}
	latency := time.Since(h.start)
	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	state := h.recorder.stateLocked(h.providerID, h.model)
	if state.inFlight > 0 {
		state.inFlight--
	}
	state.lastLatency = latency
	if outcome.Success {
		state.successes++
	} else {
		state.errors++
		state.lastErrorCategory = outcome.ErrorCategory
	}
	if outcome.ProducedMessage {
		state.messages++
	}
	if outcome.Usage != nil {
		state.inputTokens += int64(outcome.Usage.InputTokens)
		state.outputTokens += int64(outcome.Usage.OutputTokens)
		state.totalTokens += int64(outcome.Usage.TotalTokens)
	} else {
		state.unavailableUsageCount++
	}
}

// Snapshot returns a deterministic, sorted copy of every target's counters.
func (r *Recorder) Snapshot() []TargetSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]TargetSnapshot, 0, len(r.targets))
	for key, state := range r.targets {
		providerID, model := splitTargetKey(key)
		out = append(out, TargetSnapshot{
			Provider:              providerID,
			Model:                 model,
			Requests:              state.requests,
			Successes:             state.successes,
			Errors:                state.errors,
			InFlight:              state.inFlight,
			Messages:              state.messages,
			LastLatency:           state.lastLatency,
			LastErrorCategory:     state.lastErrorCategory,
			InputTokens:           state.inputTokens,
			OutputTokens:          state.outputTokens,
			TotalTokens:           state.totalTokens,
			UnavailableUsageCount: state.unavailableUsageCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// DeriveProviderState computes the small, stable display state for one
// provider from state this package does not own: existing enabled/auth
// health and whether the provider currently has any active cooldown. It
// creates no new health or cooldown state machine; it only classifies
// existing signals for display.
func DeriveProviderState(enabled, authenticated, healthError bool, hasActiveCooldown bool, inFlight int64) ProviderDisplayState {
	switch {
	case !enabled:
		return StateDisabled
	case healthError:
		return StateError
	case !authenticated:
		return StateAuthRequired
	case hasActiveCooldown:
		return StateCooldown
	case inFlight > 0:
		return StateServing
	default:
		return StateReady
	}
}
