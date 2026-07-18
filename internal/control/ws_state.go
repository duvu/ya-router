// ws_state.go composes the compact daemon state payload streamed over
// /control/v1/ws: an initial full snapshot after connect (or explicit
// snapshot.request), then state.updated deltas when provider, routing, or
// counter state changes. It reuses the existing provider/routing/telemetry
// read models rather than adding another health or cooldown state machine;
// see StateReader for the seam this package depends on.
package control

import "context"

// StateReader produces the current compact daemon state. Implementations
// must perform no long-running or blocking work: this is called on both the
// connection's request-processing goroutine (for snapshot.request) and a
// periodic background poll (for state.updated), so a slow reader delays
// state delivery but never blocks the data plane or another connection.
type StateReader interface {
	State(ctx context.Context) (WSStatePayload, error)
}

// ProviderStateView is the redacted, bounded per-provider view included in
// the WS state payload. Reason never carries a raw upstream error string.
type ProviderStateView struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
}

// RoutingTargetView reports one umbrella target's current readiness.
type RoutingTargetView struct {
	Target   string `json:"target"`
	Routable bool   `json:"routable"`
	Reason   string `json:"reason"`
}

// RoutingStateView summarizes the current/latest selected target for one
// virtual model plus why any skipped target was skipped.
type RoutingStateView struct {
	VirtualModel   string              `json:"virtual_model"`
	SelectedTarget string              `json:"selected_target,omitempty"`
	Targets        []RoutingTargetView `json:"targets"`
}

// UsageView is exact token usage, or Unavailable=true when the upstream
// response did not report it. Never estimated.
type UsageView struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	Unavailable  bool  `json:"unavailable"`
}

// TargetCountersView is one provider/model target's request/message/latency
// counters "since daemon start".
type TargetCountersView struct {
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	Requests          uint64    `json:"requests"`
	Successes         uint64    `json:"successes"`
	Errors            uint64    `json:"errors"`
	InFlight          int64     `json:"in_flight"`
	Messages          uint64    `json:"messages"`
	LastLatencyMillis int64     `json:"last_latency_millis"`
	LastErrorCategory string    `json:"last_error_category,omitempty"`
	Usage             UsageView `json:"usage"`
}

// WSStatePayload is the full body of both the "snapshot" and "state.updated"
// messages. The client treats "snapshot" as authoritative/replacing and
// "state.updated" as a merge of whatever this payload contains; the daemon
// always sends the complete current view in both cases (issue #75 rules out
// a persisted delta/replay log), so clients never need reconciliation logic
// beyond "replace with latest."
type WSStatePayload struct {
	ConfigRevision uint64               `json:"config_revision"`
	Providers      []ProviderStateView  `json:"providers"`
	Routing        []RoutingStateView   `json:"routing"`
	Counters       []TargetCountersView `json:"counters"`
}
