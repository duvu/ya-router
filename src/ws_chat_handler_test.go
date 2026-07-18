package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"

	"github.com/gorilla/websocket"
)

func wsDialerOverUnixSocket(socket string) *websocket.Dialer {
	return &websocket.Dialer{NetDialContext: unixSocketDialContext(socket)}
}

func readWSTestEnvelope(t *testing.T, conn *websocket.Conn) controlpkg.WSEnvelope {
	t.Helper()
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	var envelope controlpkg.WSEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func writeWSTestEnvelope(t *testing.T, conn *websocket.Conn, envelope controlpkg.WSEnvelope) {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func mustMarshalTest(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

// *controlpkg.WSConn has no exported constructor, so wsChatHandler cannot be
// unit-tested against a fake connection; buildWSChatRequestBody is tested in
// isolation below, and the daemon-level test further down drives the real
// type over an actual WebSocket connection.

func TestBuildWSChatRequestBody_DefaultsStreamTrueAndOmitsEmptyModel(t *testing.T) {
	body, err := buildWSChatRequestBody(controlpkg.WSChatStartPayload{Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["stream"] != true {
		t.Fatalf("stream = %v, want true", decoded["stream"])
	}
	if _, hasModel := decoded["model"]; hasModel {
		t.Fatalf("model should be omitted when payload.Model is empty, got %v", decoded["model"])
	}
}

func TestBuildWSChatRequestBody_RejectsEmptyMessages(t *testing.T) {
	if _, err := buildWSChatRequestBody(controlpkg.WSChatStartPayload{}); err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestBuildWSChatRequestBody_IncludesExplicitModel(t *testing.T) {
	body, err := buildWSChatRequestBody(controlpkg.WSChatStartPayload{Model: "thiendu", Messages: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["model"] != "thiendu" {
		t.Fatalf("model = %v, want thiendu", decoded["model"])
	}
}

// TestWSChatOverRealSocket_StreamsRouteDeltaDone drives a full chat.start
// over a real WebSocket connection against the daemon's actual control
// runtime and a mock provider, proving the WS chat path traverses the same
// dispatch used by ordinary HTTP requests: selected provider/model matches
// routing, deltas arrive in order, and exactly one terminal event is sent.
func TestWSChatOverRealSocket_StreamsRouteDeltaDone(t *testing.T) {
	clearControlEnvironment(t)
	config := defaultConfig()
	config.Routing.VirtualModels = map[string]VirtualModelConfig{}
	config.Providers.Codex.Enabled = false
	config.Providers.Kilo.Enabled = false

	provider := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			for _, chunk := range []string{"Hel", "lo"} {
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + chunk + "\"}}]}\n\n"))
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return nil
		},
	}

	runtimeManager, err := runtimepkg.NewManager(config, provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeManager.Close(context.Background()) })

	health := providerpkg.NewHealthRegistry()
	events := providerpkg.NewEventBus(16)
	providerManager, err := providerpkg.NewManager(runtimeManager, health, events, providerpkg.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	factory := providerpkg.FactoryFuncs{
		ProviderDescriptor: providerpkg.Descriptor{
			ID: ProviderCopilot, Name: "Copilot", Capabilities: []Capability{CapabilityChat}, SchemaVersion: 1,
		},
		BuildFunc: func(context.Context, any) (providerpkg.Provider, error) { return provider, nil },
	}
	if err := providerManager.RegisterFactory(factory); err != nil {
		t.Fatal(err)
	}
	if _, err := providerManager.Reconcile(context.Background(), []providerpkg.DesiredProvider{{ID: ProviderCopilot, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = providerManager.Reconcile(context.Background(), nil) })

	oldOverride := configPathOverride
	configPathOverride = t.TempDir() + "/config.json"
	t.Cleanup(func() { configPathOverride = oldOverride })

	runtime, err := newManagedControlRuntime(config, runtimeManager, providerManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.service.Shutdown(ctx)
	})

	dialer := wsDialerOverUnixSocket(runtime.unixSocket)
	conn, _, err := dialer.Dial("ws://unix/control/v1/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readWSTestEnvelope(t, conn) // hello
	readWSTestEnvelope(t, conn) // initial snapshot

	writeWSTestEnvelope(t, conn, controlpkg.WSEnvelope{
		Type:      controlpkg.WSTypeChatStart,
		RequestID: "chat-1",
		Payload:   mustMarshalTest(controlpkg.WSChatStartPayload{Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}),
	})

	route := readWSTestEnvelope(t, conn)
	if route.Type != controlpkg.WSTypeChatRoute {
		t.Fatalf("first response type = %q, want chat.route", route.Type)
	}
	var routePayload controlpkg.WSChatRoutePayload
	if err := json.Unmarshal(route.Payload, &routePayload); err != nil {
		t.Fatal(err)
	}
	if routePayload.Provider != "copilot" {
		t.Fatalf("routed provider = %q, want copilot", routePayload.Provider)
	}

	var text strings.Builder
	for {
		envelope := readWSTestEnvelope(t, conn)
		if envelope.Type == controlpkg.WSTypeChatDone {
			break
		}
		if envelope.Type != controlpkg.WSTypeChatDelta {
			t.Fatalf("unexpected message before chat.done: %+v", envelope)
		}
		var delta controlpkg.WSChatDeltaPayload
		if err := json.Unmarshal(envelope.Payload, &delta); err != nil {
			t.Fatal(err)
		}
		text.WriteString(delta.Text)
	}
	if text.String() != "Hello" {
		t.Fatalf("assembled delta text = %q, want %q", text.String(), "Hello")
	}
}

// wsChatTestFixture builds a real daemon control runtime over a mock
// provider, reused by the cancellation/error/no-active-target tests below.
type wsChatTestFixture struct {
	conn    *websocket.Conn
	runtime *managedControlRuntime
}

func newWSChatTestFixture(t *testing.T, provider providerpkg.Provider, virtualModels map[string]VirtualModelConfig) *wsChatTestFixture {
	t.Helper()
	clearControlEnvironment(t)
	config := defaultConfig()
	config.Routing.VirtualModels = virtualModels
	config.Providers.Codex.Enabled = false
	config.Providers.Kilo.Enabled = false

	providers := []providerpkg.Provider{}
	if provider != nil {
		providers = append(providers, provider)
	}
	runtimeManager, err := runtimepkg.NewManager(config, providers...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeManager.Close(context.Background()) })

	health := providerpkg.NewHealthRegistry()
	events := providerpkg.NewEventBus(16)
	providerManager, err := providerpkg.NewManager(runtimeManager, health, events, providerpkg.ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if provider != nil {
		factory := providerpkg.FactoryFuncs{
			ProviderDescriptor: providerpkg.Descriptor{
				ID: provider.ID(), Name: string(provider.ID()), Capabilities: []Capability{CapabilityChat}, SchemaVersion: 1,
			},
			BuildFunc: func(context.Context, any) (providerpkg.Provider, error) { return provider, nil },
		}
		if err := providerManager.RegisterFactory(factory); err != nil {
			t.Fatal(err)
		}
		if _, err := providerManager.Reconcile(context.Background(), []providerpkg.DesiredProvider{{ID: provider.ID(), Enabled: true}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = providerManager.Reconcile(context.Background(), nil) })

	oldOverride := configPathOverride
	configPathOverride = t.TempDir() + "/config.json"
	t.Cleanup(func() { configPathOverride = oldOverride })

	runtime, err := newManagedControlRuntime(config, runtimeManager, providerManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.service.Shutdown(ctx)
	})

	dialer := wsDialerOverUnixSocket(runtime.unixSocket)
	conn, _, err := dialer.Dial("ws://unix/control/v1/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readWSTestEnvelope(t, conn) // hello
	readWSTestEnvelope(t, conn) // initial snapshot

	return &wsChatTestFixture{conn: conn, runtime: runtime}
}

// TestWSChatCancel_StopsUpstreamContextAndProducesOneTerminalEvent proves
// chat.cancel cancels the context observed by the data-plane dispatch and
// yields exactly one terminal event (never chat.done after a cancel).
func TestWSChatCancel_StopsUpstreamContextAndProducesOneTerminalEvent(t *testing.T) {
	upstreamCanceled := make(chan struct{}, 1)
	provider := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(ctx context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-ctx.Done()
			upstreamCanceled <- struct{}{}
			return ctx.Err()
		},
	}
	fixture := newWSChatTestFixture(t, provider, map[string]VirtualModelConfig{})

	writeWSTestEnvelope(t, fixture.conn, controlpkg.WSEnvelope{
		Type:      controlpkg.WSTypeChatStart,
		RequestID: "cancel-me",
		Payload:   mustMarshalTest(controlpkg.WSChatStartPayload{Model: "github/gpt-5-mini", Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}),
	})
	route := readWSTestEnvelope(t, fixture.conn)
	if route.Type != controlpkg.WSTypeChatRoute {
		t.Fatalf("type = %q, want chat.route", route.Type)
	}

	writeWSTestEnvelope(t, fixture.conn, controlpkg.WSEnvelope{Type: controlpkg.WSTypeChatCancel})

	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream context was not canceled")
	}

	terminal := readWSTestEnvelope(t, fixture.conn)
	if terminal.Type != controlpkg.WSTypeChatError {
		t.Fatalf("terminal type = %q, want chat.error", terminal.Type)
	}
	if terminal.RequestID != "cancel-me" {
		t.Fatalf("terminal request_id = %q, want cancel-me", terminal.RequestID)
	}
}

// TestWSChatStart_FeedsTelemetry proves a chat.start over the WS path
// updates the same telemetry recorder ordinary HTTP requests feed (issue
// #72), since both traverse processProxyRequest.
func TestWSChatStart_FeedsTelemetry(t *testing.T) {
	oldRecorder := currentTelemetryRecorder()
	recorder := telemetrypkg.NewRecorder()
	setTelemetryRecorder(recorder)
	t.Cleanup(func() { setTelemetryRecorder(oldRecorder) })

	provider := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return nil
		},
	}
	fixture := newWSChatTestFixture(t, provider, map[string]VirtualModelConfig{})

	writeWSTestEnvelope(t, fixture.conn, controlpkg.WSEnvelope{
		Type:      controlpkg.WSTypeChatStart,
		RequestID: "telemetry-check",
		Payload:   mustMarshalTest(controlpkg.WSChatStartPayload{Model: "github/gpt-5-mini", Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}),
	})
	readWSTestEnvelope(t, fixture.conn) // chat.route
	readWSTestEnvelope(t, fixture.conn) // chat.delta (non-streaming response is delivered as one delta)
	done := readWSTestEnvelope(t, fixture.conn)
	if done.Type != controlpkg.WSTypeChatDone {
		t.Fatalf("type = %q, want chat.done: %+v", done.Type, done)
	}

	snap := recorder.Snapshot()
	if len(snap) != 1 || snap[0].Provider != "copilot" || snap[0].Model != "gpt-5-mini" {
		t.Fatalf("telemetry snapshot = %+v", snap)
	}
	if snap[0].Successes != 1 || snap[0].TotalTokens != 2 {
		t.Fatalf("telemetry counters = %+v", snap[0])
	}
}

// TestWSChatNoActiveTarget_ProducesTerminalErrorWithoutCallingProvider
// proves an umbrella model with no routable target yields a terminal
// chat.error and never dispatches to any provider.
func TestWSChatNoActiveTarget_ProducesTerminalErrorWithoutCallingProvider(t *testing.T) {
	var calls int
	provider := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false}, // not ready -> unroutable
		proxyFunc: func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error {
			calls++
			return nil
		},
	}
	fixture := newWSChatTestFixture(t, provider, map[string]VirtualModelConfig{
		"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
	})

	writeWSTestEnvelope(t, fixture.conn, controlpkg.WSEnvelope{
		Type:      controlpkg.WSTypeChatStart,
		RequestID: "no-target",
		Payload:   mustMarshalTest(controlpkg.WSChatStartPayload{Model: "router/auto", Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}),
	})

	terminal := readWSTestEnvelope(t, fixture.conn)
	if terminal.Type != controlpkg.WSTypeChatError {
		t.Fatalf("type = %q, want chat.error", terminal.Type)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0 (no active target must never dispatch)", calls)
	}
}
