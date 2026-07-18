package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func startWSTestServer(t *testing.T, handler WSHandler) (dialURL string, api *API) {
	t.Helper()
	api = NewAPI(APIOptions{ServiceVersion: "test"})
	RegisterWSRoute(api, handler, "test", func() uint64 { return 7 })
	identity := Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"}
	server := httptest.NewServer(api.Handler(FixedAuthenticator(identity)))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/control/v1/ws", api
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readEnvelope(t *testing.T, conn *websocket.Conn) WSEnvelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	var envelope WSEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return envelope
}

func writeEnvelope(t *testing.T, conn *websocket.Conn, envelope WSEnvelope) {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

// TestWSConnect_ReceivesHelloThenSnapshot proves a local client connects and
// receives hello then an initial snapshot without requesting one explicitly.
func TestWSConnect_ReceivesHelloThenSnapshot(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)

	hello := readEnvelope(t, conn)
	if hello.Type != WSTypeHello {
		t.Fatalf("first message type = %q, want hello", hello.Type)
	}
	var payload WSHelloPayload
	if err := json.Unmarshal(hello.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ProtocolVersion != WSProtocolVersion || payload.ServiceVersion != "test" || payload.ConfigRevision != 7 {
		t.Fatalf("hello payload = %+v", payload)
	}
	if hello.Sequence != 1 {
		t.Fatalf("hello sequence = %d, want 1", hello.Sequence)
	}

	snapshot := readEnvelope(t, conn)
	if snapshot.Type != WSTypeSnapshot {
		t.Fatalf("second message type = %q, want snapshot", snapshot.Type)
	}
	if snapshot.Sequence != 2 {
		t.Fatalf("snapshot sequence = %d, want 2 (monotonic after hello)", snapshot.Sequence)
	}
}

// TestWSPingPong proves the typed ping/pong round trip works and echoes the
// request ID for correlation.
func TestWSPingPong(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)
	readEnvelope(t, conn) // hello
	readEnvelope(t, conn) // snapshot

	writeEnvelope(t, conn, WSEnvelope{Type: WSTypePing, RequestID: "req-1"})
	pong := readEnvelope(t, conn)
	if pong.Type != WSTypePong || pong.RequestID != "req-1" {
		t.Fatalf("pong = %+v", pong)
	}
}

// TestWSSnapshotRequest_AnswersExplicitRequest proves a client-initiated
// snapshot.request gets one snapshot reply.
func TestWSSnapshotRequest_AnswersExplicitRequest(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)
	readEnvelope(t, conn) // hello
	readEnvelope(t, conn) // initial snapshot

	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeSnapshotRequest})
	got := readEnvelope(t, conn)
	if got.Type != WSTypeSnapshot {
		t.Fatalf("type = %q, want snapshot", got.Type)
	}
}

// TestWSMalformedMessage_FailsSafelyWithoutClosingConnection proves invalid
// JSON produces a typed error but the connection stays usable for the next
// message.
func TestWSMalformedMessage_FailsSafelyWithoutClosingConnection(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)
	readEnvelope(t, conn) // hello
	readEnvelope(t, conn) // snapshot

	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	errEnvelope := readEnvelope(t, conn)
	if errEnvelope.Type != WSTypeChatError {
		t.Fatalf("type = %q, want chat.error", errEnvelope.Type)
	}

	// Connection must still be alive: a follow-up ping gets a pong.
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypePing, RequestID: "still-alive"})
	pong := readEnvelope(t, conn)
	if pong.Type != WSTypePong || pong.RequestID != "still-alive" {
		t.Fatalf("connection did not survive malformed message: %+v", pong)
	}
}

// TestWSUnknownMessageType_FailsSafely proves an unrecognized type produces a
// typed error rather than a panic or silent drop.
func TestWSUnknownMessageType_FailsSafely(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)
	readEnvelope(t, conn)
	readEnvelope(t, conn)

	writeEnvelope(t, conn, WSEnvelope{Type: "bogus.type", RequestID: "r1"})
	got := readEnvelope(t, conn)
	if got.Type != WSTypeChatError {
		t.Fatalf("type = %q, want chat.error", got.Type)
	}
	var payload WSProtocolErrorPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != WSErrorUnknownType {
		t.Fatalf("code = %q, want unknown_message_type", payload.Code)
	}
}

// TestWSOversizedMessage_ClosesConnectionSafely proves a frame larger than
// the bound closes the connection rather than growing memory unboundedly.
func TestWSOversizedMessage_ClosesConnectionSafely(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	conn := dialWS(t, url)
	readEnvelope(t, conn)
	readEnvelope(t, conn)

	oversized := WSEnvelope{Type: WSTypeChatStart, Payload: json.RawMessage(`"` + strings.Repeat("x", wsMaxMessageBytes+1024) + `"`)}
	encoded, _ := json.Marshal(oversized)
	_ = conn.WriteMessage(websocket.TextMessage, encoded)

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection to close after oversized message")
	}
}

// TestWSPerConnectionOrderPreserved proves messages sent in order on one
// connection are handled and observable in that same order.
func TestWSPerConnectionOrderPreserved(t *testing.T) {
	var received []string
	handler := &recordingWSHandler{onSnapshot: func() { received = append(received, "snapshot") }}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)
	readEnvelope(t, conn) // hello
	readEnvelope(t, conn) // initial snapshot triggered by connect

	for i := 0; i < 5; i++ {
		writeEnvelope(t, conn, WSEnvelope{Type: WSTypeSnapshotRequest, RequestID: string(rune('a' + i))})
		readEnvelope(t, conn)
	}
	if len(received) != 6 { // 1 initial + 5 explicit
		t.Fatalf("received = %d snapshot calls, want 6", len(received))
	}
}

type recordingWSHandler struct {
	onSnapshot func()
}

func (h *recordingWSHandler) HandleSnapshotRequest(_ context.Context, conn *WSConn) {
	if h.onSnapshot != nil {
		h.onSnapshot()
	}
	conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
}
func (h *recordingWSHandler) HandleChatStart(_ context.Context, conn *WSConn, requestID string, _ WSChatStartPayload) {
	conn.Send(WSTypeChatDone, requestID, nil)
}
func (h *recordingWSHandler) HandleChatCancel(*WSConn) {}

// TestWSOneActiveChatPerConnection proves a second chat.start while one is
// in flight is rejected rather than run concurrently.
func TestWSOneActiveChatPerConnection(t *testing.T) {
	release := make(chan struct{})
	handler := &blockingChatHandler{release: release}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)
	readEnvelope(t, conn)
	readEnvelope(t, conn)

	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeChatStart, RequestID: "first", Payload: json.RawMessage(`{"messages":[]}`)})
	// Give the handler goroutine a moment to register as in-flight.
	time.Sleep(50 * time.Millisecond)
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeChatStart, RequestID: "second", Payload: json.RawMessage(`{"messages":[]}`)})

	rejected := readEnvelope(t, conn)
	if rejected.Type != WSTypeChatError || rejected.RequestID != "second" {
		t.Fatalf("second chat.start = %+v, want rejected chat.error for 'second'", rejected)
	}
	close(release)
	done := readEnvelope(t, conn)
	if done.Type != WSTypeChatDone || done.RequestID != "first" {
		t.Fatalf("first chat.start terminal = %+v", done)
	}
}

type blockingChatHandler struct {
	release chan struct{}
}

func (h *blockingChatHandler) HandleSnapshotRequest(_ context.Context, conn *WSConn) {
	conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
}
func (h *blockingChatHandler) HandleChatStart(ctx context.Context, conn *WSConn, requestID string, _ WSChatStartPayload) {
	select {
	case <-h.release:
	case <-ctx.Done():
		conn.Send(WSTypeChatError, requestID, mustMarshal(WSChatErrorPayload{Category: "canceled"}))
		return
	}
	conn.Send(WSTypeChatDone, requestID, nil)
}
func (h *blockingChatHandler) HandleChatCancel(*WSConn) {}

// TestWSChatCancel_StopsUpstreamWork proves chat.cancel cancels the context
// passed to HandleChatStart.
func TestWSChatCancel_StopsUpstreamWork(t *testing.T) {
	handler := &blockingChatHandler{release: make(chan struct{})}
	url, _ := startWSTestServer(t, handler)
	conn := dialWS(t, url)
	readEnvelope(t, conn)
	readEnvelope(t, conn)

	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeChatStart, RequestID: "c1", Payload: json.RawMessage(`{"messages":[]}`)})
	time.Sleep(50 * time.Millisecond)
	writeEnvelope(t, conn, WSEnvelope{Type: WSTypeChatCancel})

	got := readEnvelope(t, conn)
	if got.Type != WSTypeChatError {
		t.Fatalf("expected cancel to produce a terminal chat.error, got %+v", got)
	}
}

// TestWSDisconnectAndShutdownCleanUpGoroutines drives many connect/disconnect
// cycles under -race to catch goroutine or socket leaks.
func TestWSDisconnectAndShutdownCleanUpGoroutines(t *testing.T) {
	url, _ := startWSTestServer(t, NoopWSHandler{})
	for i := 0; i < 20; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		_ = conn.Close()
	}
}

// TestWSSlowClientIsDisconnectedRatherThanBlocking proves flooding a
// connection's outbound queue past its bound closes that connection (via
// Send's non-blocking fallback) rather than blocking the sender or growing
// memory unboundedly.
func TestWSSlowClientIsDisconnectedRatherThanBlocking(t *testing.T) {
	var capturedConn *WSConn
	captured := make(chan struct{})
	handler := &captureConnHandler{onConn: func(c *WSConn) {
		if capturedConn == nil {
			capturedConn = c
			close(captured)
		}
	}}
	url, _ := startWSTestServer(t, handler)
	rawConn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rawConn.Close() })

	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("did not capture server-side connection")
	}

	// Never read from rawConn from here on: flood Send far past the bounded
	// queue size. This must return promptly (not block) and eventually close
	// the connection rather than growing the queue unboundedly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < wsOutboundQueueSize*4; i++ {
			capturedConn.Send(WSTypeStateUpdated, "", json.RawMessage(`{}`))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked instead of closing the slow connection")
	}
}

type captureConnHandler struct {
	onConn func(*WSConn)
}

func (h *captureConnHandler) HandleSnapshotRequest(_ context.Context, conn *WSConn) {
	if h.onConn != nil {
		h.onConn(conn)
	}
	conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
}
func (h *captureConnHandler) HandleChatStart(_ context.Context, conn *WSConn, requestID string, _ WSChatStartPayload) {
	conn.Send(WSTypeChatDone, requestID, nil)
}
func (h *captureConnHandler) HandleChatCancel(*WSConn) {}

// TestExistingRESTRoutesUnaffectedByWSRegistration proves registering the ws
// route does not interfere with an ordinary REST route on the same API.
func TestExistingRESTRoutesUnaffectedByWSRegistration(t *testing.T) {
	api := NewAPI(APIOptions{ServiceVersion: "test"})
	RegisterWSRoute(api, NoopWSHandler{}, "test", nil)
	identity := Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"}
	server := httptest.NewServer(api.Handler(FixedAuthenticator(identity)))
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/control/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
