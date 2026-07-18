package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

func withTelemetryRecorder(t *testing.T) *telemetrypkg.Recorder {
	t.Helper()
	recorder := telemetrypkg.NewRecorder()
	old := currentTelemetryRecorder()
	setTelemetryRecorder(recorder)
	t.Cleanup(func() { setTelemetryRecorder(old) })
	return recorder
}

// TestProxyHandler_RecordsSuccessTelemetryWithExactUsage proves a successful
// chat completion increments requests/successes/messages and records the
// exact upstream-reported token usage (issue #72).
func TestProxyHandler_RecordsSuccessTelemetryWithExactUsage(t *testing.T) {
	recorder := withTelemetryRecorder(t)
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"gpt-5-mini","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-5-mini","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	snap := recorder.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("targets = %d, want 1: %+v", len(snap), snap)
	}
	got := snap[0]
	if got.Provider != "copilot" || got.Model != "gpt-5-mini" {
		t.Fatalf("target = %+v", got)
	}
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

// TestProxyHandler_RecordsErrorTelemetryWithoutMessage proves an upstream
// error increments errors (not successes), records a bounded error category,
// and counts usage as unavailable rather than estimating it.
func TestProxyHandler_RecordsErrorTelemetryWithoutMessage(t *testing.T) {
	recorder := withTelemetryRecorder(t)
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-5-mini","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	snap := recorder.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("targets = %d, want 1", len(snap))
	}
	got := snap[0]
	if got.Errors != 1 || got.Successes != 0 {
		t.Fatalf("counters = %+v", got)
	}
	if got.Messages != 0 {
		t.Fatalf("messages = %d, want 0", got.Messages)
	}
	if got.LastErrorCategory != telemetrypkg.ErrorCategoryRateLimited {
		t.Fatalf("error category = %q, want rate_limited", got.LastErrorCategory)
	}
	if got.UnavailableUsageCount != 1 {
		t.Fatalf("unavailable usage = %d, want 1", got.UnavailableUsageCount)
	}
}

// TestProxyHandler_TelemetryFailureNeverAltersResponse proves the HTTP result
// is unaffected when no telemetry recorder is installed (the common state for
// most tests and for the compatibility binary).
func TestProxyHandler_TelemetryFailureNeverAltersResponse(t *testing.T) {
	setTelemetryRecorder(nil)
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"gpt-5-mini","choices":[]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-5-mini","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.ID != "chatcmpl-1" {
		t.Fatalf("response body corrupted by telemetry sniffing: err=%v body=%+v", err, body)
	}
}

// TestProxyHandler_StreamingResponseUnaffectedByUsageSniffing proves the SSE
// bytes reaching the client are byte-identical whether or not the usage
// sniffer is attached.
func TestProxyHandler_StreamingResponseUnaffectedByUsageSniffing(t *testing.T) {
	withTelemetryRecorder(t)
	sseBody := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\ndata: [DONE]\n\n"
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sseBody))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-5-mini","messages":[],"stream":true}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Body.String() != sseBody {
		t.Fatalf("streamed body altered by usage sniffer:\ngot:  %q\nwant: %q", rec.Body.String(), sseBody)
	}
}

// TestTelemetry_RedactsPromptAndCompletionContent proves the recorded
// telemetry snapshot for a request carrying a secret-looking prompt and
// completion contains no trace of that content — only bounded
// provider/model identifiers, counters, and enum values.
func TestTelemetry_RedactsPromptAndCompletionContent(t *testing.T) {
	recorder := withTelemetryRecorder(t)
	const secretPrompt = "sk-super-secret-account-id-do-not-leak"
	const secretCompletion = "here is your confidential answer XYZ123"
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, r *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","model":"gpt-5-mini","choices":[{"message":{"content":"` + secretCompletion + `"}}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	reqBody := `{"model":"github/gpt-5-mini","messages":[{"role":"user","content":"` + secretPrompt + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	encoded, err := json.Marshal(recorder.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretPrompt) || strings.Contains(string(encoded), secretCompletion) {
		t.Fatalf("telemetry snapshot leaked request/response content: %s", encoded)
	}
}

func TestSniffUsageJSON_ExtractsOpenAIShape(t *testing.T) {
	usage := sniffUsageJSON([]byte(`{"id":"x","usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`))
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 5 || usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestSniffUsageJSON_ExtractsResponsesShape(t *testing.T) {
	usage := sniffUsageJSON([]byte(`{"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`))
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 20 || usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestSniffUsageJSON_KeepsLastOccurrenceInStream(t *testing.T) {
	body := `data: {"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}

data: {"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}
`
	usage := sniffUsageJSON([]byte(body))
	if usage == nil || usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v, want last occurrence (total=8)", usage)
	}
}

func TestSniffUsageJSON_AbsentReturnsNilNotZero(t *testing.T) {
	if usage := sniffUsageJSON([]byte(`{"id":"x","choices":[]}`)); usage != nil {
		t.Fatalf("usage = %+v, want nil (absent, not zero)", usage)
	}
}

func TestUsageSniffWriter_CapsAtMaxBytesWithoutError(t *testing.T) {
	rec := httptest.NewRecorder()
	sniffer := newUsageSniffWriter(rec)
	big := make([]byte, usageSniffMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if _, err := sniffer.Write(big); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if usage := sniffer.Usage(); usage != nil {
		t.Fatalf("usage = %+v, want nil once capped", usage)
	}
	if rec.Body.Len() != len(big) {
		t.Fatalf("underlying writer received %d bytes, want %d (capping must not drop client bytes)", rec.Body.Len(), len(big))
	}
}
