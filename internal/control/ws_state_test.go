package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeStateReader struct {
	payload atomic.Pointer[WSStatePayload]
	err     atomic.Pointer[error]
	calls   atomic.Int32
}

func newFakeStateReader(initial WSStatePayload) *fakeStateReader {
	r := &fakeStateReader{}
	r.payload.Store(&initial)
	return r
}

func (r *fakeStateReader) State(context.Context) (WSStatePayload, error) {
	r.calls.Add(1)
	if errPtr := r.err.Load(); errPtr != nil && *errPtr != nil {
		return WSStatePayload{}, *errPtr
	}
	return *r.payload.Load(), nil
}

func (r *fakeStateReader) set(payload WSStatePayload) {
	r.payload.Store(&payload)
}

// TestStateAwareWSHandler_SnapshotRequestReturnsCurrentState proves an
// explicit snapshot.request (and the automatic on-connect snapshot) reflects
// the current StateReader output.
func TestStateAwareWSHandler_SnapshotRequestReturnsCurrentState(t *testing.T) {
	reader := newFakeStateReader(WSStatePayload{ConfigRevision: 5, Providers: []ProviderStateView{{Provider: "copilot", State: "ready"}}})
	handler := StateAwareWSHandler{Reader: reader}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)

	readEnvelope(t, conn) // hello
	snapshot := readEnvelope(t, conn)
	if snapshot.Type != WSTypeSnapshot {
		t.Fatalf("type = %q, want snapshot", snapshot.Type)
	}
	var payload WSStatePayload
	if err := json.Unmarshal(snapshot.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ConfigRevision != 5 || len(payload.Providers) != 1 || payload.Providers[0].Provider != "copilot" {
		t.Fatalf("payload = %+v", payload)
	}

	reader.set(WSStatePayload{ConfigRevision: 6})
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeSnapshotRequest})
	second := readEnvelope(t, conn)
	var updated WSStatePayload
	if err := json.Unmarshal(second.Payload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.ConfigRevision != 6 {
		t.Fatalf("second snapshot config_revision = %d, want 6 (reflects latest reader output)", updated.ConfigRevision)
	}
}

// TestStateAwareWSHandler_ReaderFailureDoesNotCrashConnection proves a
// StateReader error still yields a protocol-valid (empty) snapshot rather
// than closing the connection or panicking.
func TestStateAwareWSHandler_ReaderFailureDoesNotCrashConnection(t *testing.T) {
	reader := newFakeStateReader(WSStatePayload{})
	failure := errors.New("read model unavailable")
	reader.err.Store(&failure)
	handler := StateAwareWSHandler{Reader: reader}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)

	readEnvelope(t, conn) // hello
	snapshot := readEnvelope(t, conn)
	if snapshot.Type != WSTypeSnapshot {
		t.Fatalf("type = %q, want snapshot even on reader failure", snapshot.Type)
	}

	// Connection must still be alive afterward.
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypePing, RequestID: "alive"})
	pong := readEnvelope(t, conn)
	if pong.Type != WSTypePong {
		t.Fatalf("connection did not survive reader failure: %+v", pong)
	}
}

// TestStateAwareWSHandler_DelegatesChatToInnerHandler proves chat messages
// still reach an inner WSHandler when one is configured.
func TestStateAwareWSHandler_DelegatesChatToInnerHandler(t *testing.T) {
	inner := &recordingWSHandler{}
	handler := StateAwareWSHandler{Reader: newFakeStateReader(WSStatePayload{}), Chat: inner}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)
	readEnvelope(t, conn)
	readEnvelope(t, conn)

	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeChatStart, RequestID: "r1", Payload: json.RawMessage(`{"messages":[]}`)})
	done := readEnvelope(t, conn)
	if done.Type != WSTypeChatDone || done.RequestID != "r1" {
		t.Fatalf("chat did not reach inner handler: %+v", done)
	}
}

// TestStateHub_BroadcastsToConnectedClientsOnChange proves BroadcastNow
// pushes state.updated to every live connection when the read state changes.
func TestStateHub_BroadcastsToConnectedClientsOnChange(t *testing.T) {
	reader := newFakeStateReader(WSStatePayload{ConfigRevision: 1})
	hub := NewStateHub(reader)
	handler := StateAwareWSHandler{Reader: reader}

	api := NewAPI(APIOptions{ServiceVersion: "test"})
	RegisterWSRoute(api, handler, hub, "test", nil)
	identity := Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"}
	server := httptest.NewServer(api.Handler(FixedAuthenticator(identity)))
	t.Cleanup(server.Close)
	dialURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/control/v1/ws"

	conn := dialWS(t, dialURL)
	readEnvelope(t, conn) // hello
	readEnvelope(t, conn) // initial snapshot

	// Give the handler goroutine time to register with the hub before the
	// first broadcast (registration happens on the same goroutine that calls
	// serve(), immediately after upgrade — this sleep guards against a slow
	// CI scheduler, not a real race in the implementation).
	time.Sleep(20 * time.Millisecond)

	reader.set(WSStatePayload{ConfigRevision: 2})
	hub.BroadcastNow(context.Background())

	update := readEnvelope(t, conn)
	if update.Type != WSTypeStateUpdated {
		t.Fatalf("type = %q, want state.updated", update.Type)
	}
	var payload WSStatePayload
	if err := json.Unmarshal(update.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ConfigRevision != 2 {
		t.Fatalf("config_revision = %d, want 2", payload.ConfigRevision)
	}
}

// TestStateHub_CoalescesUnchangedState proves BroadcastNow does not send a
// duplicate state.updated when the underlying state has not changed between
// polls.
func TestStateHub_CoalescesUnchangedState(t *testing.T) {
	reader := newFakeStateReader(WSStatePayload{ConfigRevision: 1})
	hub := NewStateHub(reader)
	handler := StateAwareWSHandler{Reader: reader}

	api := NewAPI(APIOptions{ServiceVersion: "test"})
	RegisterWSRoute(api, handler, hub, "test", nil)
	identity := Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"}
	server := httptest.NewServer(api.Handler(FixedAuthenticator(identity)))
	t.Cleanup(server.Close)
	dialURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/control/v1/ws"

	conn := dialWS(t, dialURL)
	readEnvelope(t, conn)
	readEnvelope(t, conn)
	time.Sleep(20 * time.Millisecond)

	// The first broadcast always sends (nothing has been observed yet); the
	// next two must coalesce since the state did not change.
	hub.BroadcastNow(context.Background())
	first := readEnvelope(t, conn)
	if first.Type != WSTypeStateUpdated {
		t.Fatalf("type = %q, want state.updated for the first observed broadcast", first.Type)
	}

	hub.BroadcastNow(context.Background())
	hub.BroadcastNow(context.Background())

	// No further message should have been enqueued: prove it by racing a
	// ping/pong, which must arrive first because nothing else was sent after
	// the first broadcast above.
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypePing, RequestID: "probe"})
	next := readEnvelope(t, conn)
	if next.Type != WSTypePong {
		t.Fatalf("expected no coalesced state.updated before pong, got %+v", next)
	}
}

// TestStateHub_DisconnectUnregistersConnection proves a closed connection
// stops receiving broadcasts and BroadcastNow tolerates it (no panic, no
// leak of a reference to a dead connection across repeated broadcasts).
func TestStateHub_DisconnectUnregistersConnection(t *testing.T) {
	reader := newFakeStateReader(WSStatePayload{ConfigRevision: 1})
	hub := NewStateHub(reader)
	handler := StateAwareWSHandler{Reader: reader}

	api := NewAPI(APIOptions{ServiceVersion: "test"})
	RegisterWSRoute(api, handler, hub, "test", nil)
	identity := Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"}
	server := httptest.NewServer(api.Handler(FixedAuthenticator(identity)))
	t.Cleanup(server.Close)
	dialURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/control/v1/ws"

	conn, _, err := websocket.DefaultDialer.Dial(dialURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		reader.set(WSStatePayload{ConfigRevision: uint64(time.Now().UnixNano())})
		hub.BroadcastNow(context.Background())
		if len(hub.snapshot()) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("hub did not unregister the closed connection in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
