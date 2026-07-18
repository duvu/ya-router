package yarouter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	controlpkg "github.com/duvu/ya-router/internal/control"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"

	"github.com/gorilla/websocket"
)

func unixSocketDialContext(socket string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
}

func unixSocketTransport(socket string) *http.Transport {
	return &http.Transport{DialContext: unixSocketDialContext(socket)}
}

// TestManagedControlRuntime_ServesWSOverConfiguredUnixSocket proves the
// daemon-wired /control/v1/ws endpoint is reachable over the real,
// production-default Unix control socket, and that ordinary REST resources
// (e.g. /control/v1/meta) remain reachable on the same socket alongside it
// (issue #74's "REST/SSE regressions remain green" requirement).
func TestManagedControlRuntime_ServesWSOverConfiguredUnixSocket(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })

	config := defaultConfig()
	runtimeManager, err := runtimepkg.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeManager.Close(context.Background()) })
	providerManager, err := newProviderManager(config, runtimeManager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = providerManager.Reconcile(context.Background(), nil) })

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

	socketURL := "http://unix/control/v1/meta"
	httpClient := &http.Client{Transport: unixSocketTransport(runtime.unixSocket)}
	resp, err := httpClient.Get(socketURL)
	if err != nil {
		t.Fatalf("GET /control/v1/meta over unix socket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("meta status = %d, want 200", resp.StatusCode)
	}

	dialer := &websocket.Dialer{NetDialContext: unixSocketDialContext(runtime.unixSocket)}
	conn, _, err := dialer.Dial("ws://unix/control/v1/ws", nil)
	if err != nil {
		t.Fatalf("dial control ws over unix socket: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	var envelope controlpkg.WSEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != controlpkg.WSTypeHello {
		t.Fatalf("first message type = %q, want hello", envelope.Type)
	}

	_, snapshotData, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initial snapshot: %v", err)
	}
	var snapshotEnvelope controlpkg.WSEnvelope
	if err := json.Unmarshal(snapshotData, &snapshotEnvelope); err != nil {
		t.Fatal(err)
	}
	if snapshotEnvelope.Type != controlpkg.WSTypeSnapshot {
		t.Fatalf("second message type = %q, want snapshot", snapshotEnvelope.Type)
	}
	var initialState controlpkg.WSStatePayload
	if err := json.Unmarshal(snapshotEnvelope.Payload, &initialState); err != nil {
		t.Fatal(err)
	}
	foundCopilot := false
	for _, p := range initialState.Providers {
		if p.Provider == "copilot" {
			foundCopilot = true
		}
	}
	if !foundCopilot {
		t.Fatalf("initial snapshot missing copilot provider state: %+v", initialState.Providers)
	}

	// Drive the same background broadcast the daemon runs on a timer
	// (stateHubPollInterval) to prove a live state.updated reaches this real
	// client over the real Unix socket without any REST polling.
	runtime.stateHub.BroadcastNow(context.Background())
	_, updateData, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read state.updated: %v", err)
	}
	var updateEnvelope controlpkg.WSEnvelope
	if err := json.Unmarshal(updateData, &updateEnvelope); err != nil {
		t.Fatal(err)
	}
	if updateEnvelope.Type != controlpkg.WSTypeStateUpdated {
		t.Fatalf("third message type = %q, want state.updated", updateEnvelope.Type)
	}
	if updateEnvelope.Sequence <= snapshotEnvelope.Sequence {
		t.Fatalf("state.updated sequence %d did not advance past snapshot sequence %d", updateEnvelope.Sequence, snapshotEnvelope.Sequence)
	}
}
