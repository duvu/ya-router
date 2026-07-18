// ws_client.go is the small typed WebSocket client the Bubble Tea TUI (#79)
// uses for live status and chat. It reuses the same Profile the REST Client
// uses for connection info, but only over the local Unix-socket transport
// (see NewWSClient); remote WSS/mTLS is out of scope for this issue.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duvu/ya-router/internal/control"
	"github.com/gorilla/websocket"
)

const (
	// wsClientOutboundQueueSize bounds how many not-yet-sent client->daemon
	// messages may be queued. It is intentionally small: chat.start/
	// chat.cancel/snapshot.request/ping are all cheap, low-frequency
	// messages, so a full queue means the connection is unhealthy.
	wsClientOutboundQueueSize = 32
	// wsClientReconnectBaseDelay/MaxDelay bound retry backoff for transport
	// disconnects. Retries are for local Unix-socket hiccups (e.g. daemon
	// restart), not remote network conditions.
	wsClientReconnectBaseDelay = 500 * time.Millisecond
	wsClientReconnectMaxDelay  = 10 * time.Second
)

// WSEventType mirrors the daemon message types the TUI cares about, plus
// client-local lifecycle events (Connected/Disconnected/NonRetryable) that
// never appear on the wire.
type WSEventType string

const (
	WSEventConnected    WSEventType = "connected"
	WSEventDisconnected WSEventType = "disconnected"
	WSEventNonRetryable WSEventType = "non_retryable_error"
	WSEventSnapshot     WSEventType = "snapshot"
	WSEventStateUpdated WSEventType = "state_updated"
	WSEventChatRoute    WSEventType = "chat_route"
	WSEventChatDelta    WSEventType = "chat_delta"
	WSEventChatDone     WSEventType = "chat_done"
	WSEventChatError    WSEventType = "chat_error"
)

// WSEvent is the one typed value the TUI receives from Events(). RequestID
// correlates chat events with the chat.start that caused them (empty for
// snapshot/state.updated/lifecycle events).
type WSEvent struct {
	Type      WSEventType
	RequestID string
	State     *control.WSStatePayload
	Route     *control.WSChatRoutePayload
	Delta     *control.WSChatDeltaPayload
	Done      *control.WSChatDonePayload
	ChatErr   *control.WSChatErrorPayload
	Err       error
}

// ErrIncompatibleProtocolVersion is a non-retryable error: the daemon speaks
// a WS protocol version this client does not support. Retrying a dial
// cannot fix it — the client or daemon binary needs to change.
var ErrIncompatibleProtocolVersion = fmt.Errorf("daemon WS protocol version is incompatible with this client")

// WSClient is a small typed WebSocket client for /control/v1/ws. It owns one
// reader and one writer goroutine, retries local transport disconnects with
// bounded backoff, and marks any chat interrupted by a disconnect as failed
// locally rather than ever resubmitting it automatically.
type WSClient struct {
	profile Profile

	events chan WSEvent

	mu           sync.Mutex
	closed       bool
	conn         *websocket.Conn
	outbound     chan control.WSEnvelope
	activeChatID string

	stopOnce sync.Once
	done     chan struct{}
}

// NewWSClient builds a client for profile without connecting. Call Connect
// to establish the connection and start delivering Events().
func NewWSClient(profile Profile) (*WSClient, error) {
	if profile.Transport == "" {
		profile.Transport = TransportUnix
	}
	if profile.Transport != TransportUnix {
		return nil, fmt.Errorf("ws client supports only the local unix transport, got %q", profile.Transport)
	}
	if strings.TrimSpace(profile.Socket) == "" {
		return nil, fmt.Errorf("unix transport requires a socket path")
	}
	return &WSClient{
		profile: profile,
		events:  make(chan WSEvent, 64),
		done:    make(chan struct{}),
	}, nil
}

// Events returns the channel of daemon/lifecycle events. It is closed after
// Close completes.
func (c *WSClient) Events() <-chan WSEvent { return c.events }

// Connect starts the connect/reconnect loop in the background and returns
// immediately; connection state is observed through Events().
func (c *WSClient) Connect(ctx context.Context) {
	go c.run(ctx)
}

// Close stops the client: no further reconnect attempts, the outbound queue
// stops accepting new sends, and the underlying connection (if any) is
// closed. Close blocks until the internal goroutines have exited, so no
// socket or goroutine survives it.
func (c *WSClient) Close() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		conn := c.conn
		c.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	})
	<-c.done
}

// RequestSnapshot sends snapshot.request. It returns false when not
// currently connected; the next reconnect already triggers a fresh
// snapshot automatically, so callers need not retry this themselves.
func (c *WSClient) RequestSnapshot() bool {
	return c.send(control.WSEnvelope{Type: control.WSTypeSnapshotRequest})
}

// StartChat sends chat.start with the given request ID and message payload.
// It returns false when not currently connected.
func (c *WSClient) StartChat(requestID string, payload control.WSChatStartPayload) bool {
	c.mu.Lock()
	c.activeChatID = requestID
	c.mu.Unlock()
	return c.send(control.WSEnvelope{Type: control.WSTypeChatStart, RequestID: requestID, Payload: mustMarshalWS(payload)})
}

// CancelChat sends chat.cancel for the connection's active chat.
func (c *WSClient) CancelChat() bool {
	return c.send(control.WSEnvelope{Type: control.WSTypeChatCancel})
}

func (c *WSClient) send(envelope control.WSEnvelope) bool {
	c.mu.Lock()
	outbound := c.outbound
	closed := c.closed
	c.mu.Unlock()
	if closed || outbound == nil {
		return false
	}
	select {
	case outbound <- envelope:
		return true
	default:
		return false
	}
}

func (c *WSClient) run(ctx context.Context) {
	defer close(c.done)
	defer close(c.events)
	attempt := 0
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed || ctx.Err() != nil {
			return
		}
		err := c.connectOnce(ctx)
		if err == ErrIncompatibleProtocolVersion {
			c.emit(WSEvent{Type: WSEventNonRetryable, Err: err})
			return
		}
		c.emit(WSEvent{Type: WSEventDisconnected, Err: err})
		c.markActiveChatInterrupted()

		attempt++
		delay := backoffDelay(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// connectOnce dials, marks the connection live, runs read/write loops until
// either fails, then tears the connection down. It returns the error that
// ended the connection (nil for a clean close).
func (c *WSClient) connectOnce(ctx context.Context) error {
	dialer := &websocket.Dialer{
		NetDialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(dialCtx, "unix", c.profile.Socket)
		},
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{}
	header.Set(control.ClientVersionHeader, ClientVersion)
	conn, _, err := dialer.Dial("ws://ya-router-control/control/v1/ws", header)
	if err != nil {
		return err
	}

	outbound := make(chan control.WSEnvelope, wsClientOutboundQueueSize)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	c.conn = conn
	c.outbound = outbound
	c.mu.Unlock()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		c.writeLoop(conn, outbound)
	}()

	readErr := c.readLoop(ctx, conn)

	_ = conn.Close()
	close(outbound)
	<-writerDone

	c.mu.Lock()
	c.conn = nil
	c.outbound = nil
	c.mu.Unlock()
	return readErr
}

func (c *WSClient) writeLoop(conn *websocket.Conn, outbound <-chan control.WSEnvelope) {
	for envelope := range outbound {
		encoded := mustMarshalWS(envelope)
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, encoded); err != nil {
			return
		}
	}
}

// readLoop dispatches decoded frames and returns the error that ended the
// connection. The very first frame must be "hello"; a protocol-version
// mismatch there is reported as ErrIncompatibleProtocolVersion so run()
// stops retrying instead of reconnecting forever against an incompatible
// daemon.
func (c *WSClient) readLoop(ctx context.Context, conn *websocket.Conn) error {
	first := true
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var envelope control.WSEnvelope
		if err := unmarshalWS(data, &envelope); err != nil {
			continue
		}
		if first {
			first = false
			if envelope.Type == control.WSTypeHello {
				var hello control.WSHelloPayload
				if unmarshalWS(envelope.Payload, &hello) == nil && hello.ProtocolVersion != control.WSProtocolVersion {
					return ErrIncompatibleProtocolVersion
				}
				c.emit(WSEvent{Type: WSEventConnected})
				continue
			}
		}
		c.dispatch(envelope)
	}
}

func (c *WSClient) dispatch(envelope control.WSEnvelope) {
	switch envelope.Type {
	case control.WSTypeSnapshot:
		var payload control.WSStatePayload
		if unmarshalWS(envelope.Payload, &payload) == nil {
			c.emit(WSEvent{Type: WSEventSnapshot, RequestID: envelope.RequestID, State: &payload})
		}
	case control.WSTypeStateUpdated:
		var payload control.WSStatePayload
		if unmarshalWS(envelope.Payload, &payload) == nil {
			c.emit(WSEvent{Type: WSEventStateUpdated, RequestID: envelope.RequestID, State: &payload})
		}
	case control.WSTypeChatRoute:
		var payload control.WSChatRoutePayload
		if unmarshalWS(envelope.Payload, &payload) == nil {
			c.emit(WSEvent{Type: WSEventChatRoute, RequestID: envelope.RequestID, Route: &payload})
		}
	case control.WSTypeChatDelta:
		var payload control.WSChatDeltaPayload
		if unmarshalWS(envelope.Payload, &payload) == nil {
			c.emit(WSEvent{Type: WSEventChatDelta, RequestID: envelope.RequestID, Delta: &payload})
		}
	case control.WSTypeChatDone:
		c.clearActiveChat(envelope.RequestID)
		var payload control.WSChatDonePayload
		_ = unmarshalWS(envelope.Payload, &payload)
		c.emit(WSEvent{Type: WSEventChatDone, RequestID: envelope.RequestID, Done: &payload})
	case control.WSTypeChatError:
		c.clearActiveChat(envelope.RequestID)
		var payload control.WSChatErrorPayload
		_ = unmarshalWS(envelope.Payload, &payload)
		c.emit(WSEvent{Type: WSEventChatError, RequestID: envelope.RequestID, ChatErr: &payload})
	case control.WSTypePong:
		// No TUI-visible event; pong is a transport-level ack.
	}
}

func (c *WSClient) clearActiveChat(requestID string) {
	c.mu.Lock()
	if c.activeChatID == requestID {
		c.activeChatID = ""
	}
	c.mu.Unlock()
}

// markActiveChatInterrupted emits one chat_error for whatever chat was in
// flight when the connection dropped, so the TUI marks it interrupted
// locally and never implies it will resume automatically.
func (c *WSClient) markActiveChatInterrupted() {
	c.mu.Lock()
	requestID := c.activeChatID
	c.activeChatID = ""
	c.mu.Unlock()
	if requestID == "" {
		return
	}
	c.emit(WSEvent{
		Type:      WSEventChatError,
		RequestID: requestID,
		ChatErr:   &control.WSChatErrorPayload{Category: "interrupted", Message: "connection lost before this chat completed"},
	})
}

func (c *WSClient) emit(event WSEvent) {
	select {
	case c.events <- event:
	default:
		// The TUI consumer is expected to drain promptly; dropping a
		// lifecycle event under sustained backpressure is preferable to
		// blocking the reader loop and stalling the whole connection.
	}
}

func backoffDelay(attempt int) time.Duration {
	delay := wsClientReconnectBaseDelay
	for i := 1; i < attempt && delay < wsClientReconnectMaxDelay; i++ {
		delay *= 2
	}
	if delay > wsClientReconnectMaxDelay {
		delay = wsClientReconnectMaxDelay
	}
	return delay
}

func mustMarshalWS(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func unmarshalWS(data []byte, out any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
