package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/control"
)

// startTestWSServer serves a real control API with the WS route over a Unix
// socket, mirroring how the daemon wires it in src/control_runtime.go, and
// returns the socket path.
func startTestWSServer(t *testing.T, handler control.WSHandler) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	api := control.NewAPI(control.APIOptions{ServiceVersion: "test"})
	control.RegisterWSRoute(api, handler, nil, "test", func() uint64 { return 1 })
	identity := control.Identity{Subject: "local:test", Role: control.RoleAdmin, Source: "test"}
	server := &http.Server{Handler: api.Handler(control.FixedAuthenticator(identity))}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = os.Remove(socket)
	})
	return socket
}

func waitForEvent(t *testing.T, events <-chan WSEvent, want WSEventType, timeout time.Duration) WSEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("events channel closed before %q was observed", want)
			}
			if event.Type == want {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", want)
		}
	}
}

func TestWSClient_ConnectReceivesSnapshot(t *testing.T) {
	socket := startTestWSServer(t, control.NoopWSHandler{})
	client, err := NewWSClient(Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.Connect(ctx)
	t.Cleanup(client.Close)

	waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)
	snapshot := waitForEvent(t, client.Events(), WSEventSnapshot, 3*time.Second)
	if snapshot.State == nil {
		t.Fatal("snapshot event carries no state")
	}
}

func TestWSClient_RequestSnapshotAfterConnect(t *testing.T) {
	socket := startTestWSServer(t, control.NoopWSHandler{})
	client, err := NewWSClient(Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.Connect(ctx)
	t.Cleanup(client.Close)

	waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)
	waitForEvent(t, client.Events(), WSEventSnapshot, 3*time.Second) // initial, on connect

	if !client.RequestSnapshot() {
		t.Fatal("RequestSnapshot returned false while connected")
	}
	waitForEvent(t, client.Events(), WSEventSnapshot, 3*time.Second) // explicit
}

// chatEchoHandler answers chat.start with one chat.route then one
// chat.done, letting tests exercise the typed chat event path without a
// full daemon.
type chatEchoHandler struct{}

func (chatEchoHandler) HandleSnapshotRequest(_ context.Context, conn *control.WSConn) {
	conn.Send(control.WSTypeSnapshot, "", json.RawMessage(`{}`))
}
func (chatEchoHandler) HandleChatStart(_ context.Context, conn *control.WSConn, requestID string, _ control.WSChatStartPayload) {
	routePayload, _ := json.Marshal(control.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	conn.Send(control.WSTypeChatRoute, requestID, routePayload)
	deltaPayload, _ := json.Marshal(control.WSChatDeltaPayload{Text: "hi"})
	conn.Send(control.WSTypeChatDelta, requestID, deltaPayload)
	conn.Send(control.WSTypeChatDone, requestID, nil)
}
func (chatEchoHandler) HandleChatCancel(*control.WSConn) {}

func TestWSClient_StartChatReceivesRouteDeltaDone(t *testing.T) {
	socket := startTestWSServer(t, chatEchoHandler{})
	client, err := NewWSClient(Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.Connect(ctx)
	t.Cleanup(client.Close)

	waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)
	waitForEvent(t, client.Events(), WSEventSnapshot, 3*time.Second)

	if !client.StartChat("chat-1", control.WSChatStartPayload{Messages: json.RawMessage(`[]`)}) {
		t.Fatal("StartChat returned false while connected")
	}

	route := waitForEvent(t, client.Events(), WSEventChatRoute, 3*time.Second)
	if route.RequestID != "chat-1" || route.Route == nil || route.Route.Provider != "copilot" {
		t.Fatalf("route event = %+v", route)
	}
	delta := waitForEvent(t, client.Events(), WSEventChatDelta, 3*time.Second)
	if delta.Delta == nil || delta.Delta.Text != "hi" {
		t.Fatalf("delta event = %+v", delta)
	}
	done := waitForEvent(t, client.Events(), WSEventChatDone, 3*time.Second)
	if done.RequestID != "chat-1" {
		t.Fatalf("done event = %+v", done)
	}
}

// trackingListener records every accepted connection so a test can force-
// close them directly. A WebSocket upgrade hijacks its net.Conn out of
// http.Server's bookkeeping, so http.Server.Shutdown alone cannot terminate
// an already-upgraded connection; tests that need to simulate a mid-session
// drop must close the accepted conn themselves.
type trackingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, conn)
	l.mu.Unlock()
	return conn, nil
}

func (l *trackingListener) closeAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, conn := range l.conns {
		_ = conn.Close()
	}
	l.conns = nil
}

// TestWSClient_ReconnectAfterServerRestartRefreshesSnapshot proves a
// transport disconnect is retried and a reconnect fetches a fresh snapshot
// rather than replaying anything.
func TestWSClient_ReconnectAfterServerRestartRefreshesSnapshot(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	rawListener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	tracking := &trackingListener{Listener: rawListener}
	api := control.NewAPI(control.APIOptions{ServiceVersion: "test"})
	control.RegisterWSRoute(api, control.NoopWSHandler{}, nil, "test", func() uint64 { return 1 })
	identity := control.Identity{Subject: "local:test", Role: control.RoleAdmin, Source: "test"}
	server := &http.Server{Handler: api.Handler(control.FixedAuthenticator(identity))}
	go func() { _ = server.Serve(tracking) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	client, err := NewWSClient(Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.Connect(ctx)
	t.Cleanup(client.Close)

	waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)
	waitForEvent(t, client.Events(), WSEventSnapshot, 3*time.Second)

	// Simulate a dropped connection (e.g. daemon restart) without tearing
	// down the listener, then prove the client reconnects and fetches a
	// fresh snapshot on its own.
	tracking.closeAll()

	waitForEvent(t, client.Events(), WSEventDisconnected, 5*time.Second)
	waitForEvent(t, client.Events(), WSEventConnected, 15*time.Second)
	waitForEvent(t, client.Events(), WSEventSnapshot, 5*time.Second)
}

// TestWSClient_CloseStopsGoroutinesAndSocket drives connect/close cycles
// under -race to catch leaked goroutines or sockets.
func TestWSClient_CloseStopsGoroutinesAndSocket(t *testing.T) {
	socket := startTestWSServer(t, control.NoopWSHandler{})
	for i := 0; i < 5; i++ {
		client, err := NewWSClient(Profile{Socket: socket})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		client.Connect(ctx)
		waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)
		client.Close()
		cancel()
	}
}

// TestWSClient_ConcurrentStartCancelCloseIsRaceSafe exercises concurrent
// StartChat/CancelChat/Close calls; run with -race.
func TestWSClient_ConcurrentStartCancelCloseIsRaceSafe(t *testing.T) {
	socket := startTestWSServer(t, chatEchoHandler{})
	client, err := NewWSClient(Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client.Connect(ctx)
	waitForEvent(t, client.Events(), WSEventConnected, 3*time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			client.StartChat("race", control.WSChatStartPayload{Messages: json.RawMessage(`[]`)})
			client.CancelChat()
		}
	}()
	<-done
	client.Close()
}

func TestNewWSClient_RejectsNonUnixTransport(t *testing.T) {
	if _, err := NewWSClient(Profile{Transport: TransportHTTPS, Address: "example.com:443"}); err == nil {
		t.Fatal("expected error for non-unix transport")
	}
}

func TestNewWSClient_RejectsEmptySocket(t *testing.T) {
	if _, err := NewWSClient(Profile{}); err == nil {
		t.Fatal("expected error for empty socket path")
	}
}
