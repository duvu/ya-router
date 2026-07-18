// ws_server.go implements the minimal local WebSocket endpoint
// (/control/v1/ws) for interactive TUI chat and live state. It reuses the
// existing local Unix-socket trust/permission model: the endpoint is
// registered on the same *API as every REST resource, so it inherits the
// same authentication middleware, and requires no new listener.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// wsMaxMessageBytes bounds one client->daemon frame. Larger frames are
	// rejected and the connection is closed rather than growing memory
	// unboundedly for a misbehaving client.
	wsMaxMessageBytes = 64 * 1024
	// wsOutboundQueueSize bounds how many daemon->client messages may be
	// queued before a persistently slow client is disconnected.
	wsOutboundQueueSize = 256
	wsPingInterval      = 20 * time.Second
	wsPongWait          = 60 * time.Second
	wsWriteWait         = 10 * time.Second
)

// WSHandler implements the business logic behind each supported client
// message. Handlers run on the connection's single request-processing
// goroutine (see WSConn.serve): a slow handler delays that one connection's
// next message, never the data plane or another connection.
type WSHandler interface {
	// HandleSnapshotRequest responds to snapshot.request (and is also called
	// once automatically right after hello) by sending one snapshot message
	// via conn.Send.
	HandleSnapshotRequest(ctx context.Context, conn *WSConn)
	// HandleChatStart begins one chat.start. Implementations must send
	// exactly one terminal chat.done or chat.error (see WSConn.Send) and may
	// send any number of chat.route/chat.delta first. Called synchronously;
	// long-running work must be spun into its own goroutine using conn's
	// context if it should not block subsequent messages on this connection.
	HandleChatStart(ctx context.Context, conn *WSConn, requestID string, payload WSChatStartPayload)
	// HandleChatCancel cancels the connection's active chat, if any.
	HandleChatCancel(conn *WSConn)
}

// NoopWSHandler is a minimal WSHandler that answers every request with a
// protocol-valid but empty response. It exists so the transport
// (issue #74) is independently testable before the snapshot (#75) and chat
// (#76) business logic lands.
type NoopWSHandler struct{}

func (NoopWSHandler) HandleSnapshotRequest(_ context.Context, conn *WSConn) {
	conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
}

func (NoopWSHandler) HandleChatStart(_ context.Context, conn *WSConn, requestID string, _ WSChatStartPayload) {
	conn.Send(WSTypeChatError, requestID, mustMarshal(WSChatErrorPayload{Category: "unsupported_capability", Message: "chat is not yet available"}))
}

func (NoopWSHandler) HandleChatCancel(*WSConn) {}

// RegisterWSRoute adds GET /control/v1/ws. The route runs through the same
// authentication/audit/request-ID middleware as every other control
// resource; any authenticated viewer may connect (chat mutates no
// configuration). hub may be nil (e.g. in tests exercising only the
// chat/protocol surface); when non-nil, every connection registers with it
// for the lifetime of the connection so StateHub.BroadcastNow can reach it.
func RegisterWSRoute(api *API, handler WSHandler, hub *StateHub, serviceVersion string, configRevision func() uint64) {
	if handler == nil {
		handler = NoopWSHandler{}
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(*http.Request) bool { return true },
	}
	api.Handle(http.MethodGet, "/control/v1/ws", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identity, _ := IdentityFromContext(request.Context())
		wsConn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			// Upgrade already wrote an HTTP error response; nothing else to do.
			return
		}
		revision := uint64(0)
		if configRevision != nil {
			revision = configRevision()
		}
		conn := newWSConn(wsConn, identity)
		if hub != nil {
			hub.register(conn)
			defer hub.unregister(conn)
		}
		conn.serve(request.Context(), handler, serviceVersion, revision)
	}))
}

// WSConn is one live /control/v1/ws connection: one reader goroutine and one
// writer goroutine over a bounded outbound queue, plus the request-processing
// goroutine that decodes client frames and invokes WSHandler. All three stop
// together on read/write error, protocol violation, or context
// cancellation (connection close or daemon shutdown).
type WSConn struct {
	identity Identity
	conn     *websocket.Conn

	outbound  chan wsOutboundFrame
	closed    chan struct{}
	closeOnce sync.Once

	seqMu sync.Mutex
	seq   uint64

	chatMu     sync.Mutex
	chatCancel context.CancelFunc
}

type wsOutboundFrame struct {
	envelope WSEnvelope
}

func newWSConn(conn *websocket.Conn, identity Identity) *WSConn {
	return &WSConn{
		identity: identity,
		conn:     conn,
		outbound: make(chan wsOutboundFrame, wsOutboundQueueSize),
		closed:   make(chan struct{}),
	}
}

// Identity returns the authenticated identity that established this
// connection.
func (c *WSConn) Identity() Identity { return c.identity }

// Send enqueues one daemon->client message. It assigns the next monotonic
// sequence number for this connection. If the outbound queue is full (a
// persistently slow client), the connection is closed rather than blocking
// the caller or growing memory unboundedly; Send never blocks.
func (c *WSConn) Send(messageType WSMessageType, requestID string, payload json.RawMessage) {
	c.seqMu.Lock()
	c.seq++
	sequence := c.seq
	c.seqMu.Unlock()
	envelope := WSEnvelope{Type: messageType, RequestID: requestID, Sequence: sequence, Payload: payload}
	select {
	case c.outbound <- wsOutboundFrame{envelope: envelope}:
	case <-c.closed:
	default:
		// Outbound queue is full: this client cannot keep up. Close rather
		// than block or grow the queue unboundedly.
		c.Close()
	}
}

// Close closes the connection exactly once, unblocking its reader, writer,
// and any handler goroutine watching Context().Done().
func (c *WSConn) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

// serve runs the connection until it closes. It starts the writer goroutine,
// sends hello, requests an initial snapshot, then reads client frames on the
// calling goroutine until read failure, protocol violation, or ctx
// cancellation. It blocks until the connection is fully torn down, so
// callers (the HTTP handler goroutine) naturally observe connection
// lifetime.
func (c *WSConn) serve(ctx context.Context, handler WSHandler, serviceVersion string, configRevision uint64) {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.Close()

	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		c.writeLoop(connCtx)
	}()
	go func() {
		select {
		case <-connCtx.Done():
			c.Close()
		case <-c.closed:
		}
	}()

	c.conn.SetReadLimit(wsMaxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	c.Send(WSTypeHello, "", mustMarshal(WSHelloPayload{ProtocolVersion: WSProtocolVersion, ServiceVersion: serviceVersion, ConfigRevision: configRevision}))
	handler.HandleSnapshotRequest(connCtx, c)

	c.readLoop(connCtx, handler)
	cancel()
	c.Close()
	writerDone.Wait()
}

// readLoop decodes and dispatches client frames in order on the calling
// goroutine, preserving per-connection message order. It returns when the
// connection closes.
func (c *WSConn) readLoop(ctx context.Context, handler WSHandler) {
	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var envelope WSEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.Send(WSTypeChatError, "", mustMarshal(WSProtocolErrorPayload{Code: WSErrorMalformedMessage, Message: "message is not valid JSON"}))
			continue
		}
		switch envelope.Type {
		case WSTypePing:
			c.Send(WSTypePong, envelope.RequestID, nil)
		case WSTypeSnapshotRequest:
			handler.HandleSnapshotRequest(ctx, c)
		case WSTypeChatStart:
			var payload WSChatStartPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				c.Send(WSTypeChatError, envelope.RequestID, mustMarshal(WSChatErrorPayload{Category: "invalid_request", Message: "chat.start payload is malformed"}))
				continue
			}
			c.startChat(ctx, handler, envelope.RequestID, payload)
		case WSTypeChatCancel:
			c.cancelChat()
			handler.HandleChatCancel(c)
		default:
			c.Send(WSTypeChatError, envelope.RequestID, mustMarshal(WSProtocolErrorPayload{Code: WSErrorUnknownType, Message: "unsupported message type"}))
		}
	}
}

// startChat enforces "one active chat per connection": a chat.start while
// another is in flight is rejected rather than queued or run concurrently.
// It derives a per-chat cancelable context from ctx so chat.cancel,
// disconnect, or daemon shutdown all stop the same upstream work.
func (c *WSConn) startChat(ctx context.Context, handler WSHandler, requestID string, payload WSChatStartPayload) {
	c.chatMu.Lock()
	if c.chatCancel != nil {
		c.chatMu.Unlock()
		c.Send(WSTypeChatError, requestID, mustMarshal(WSProtocolErrorPayload{Code: WSErrorChatInProgress, Message: "a chat is already in progress on this connection"}))
		return
	}
	chatCtx, cancel := context.WithCancel(ctx)
	c.chatCancel = cancel
	c.chatMu.Unlock()

	go func() {
		defer func() {
			c.chatMu.Lock()
			if c.chatCancel != nil {
				c.chatCancel()
				c.chatCancel = nil
			}
			c.chatMu.Unlock()
		}()
		handler.HandleChatStart(chatCtx, c, requestID, payload)
	}()
}

func (c *WSConn) cancelChat() {
	c.chatMu.Lock()
	cancel := c.chatCancel
	c.chatCancel = nil
	c.chatMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// writeLoop is the connection's single writer: WebSocket connections are not
// safe for concurrent writes, so every outbound frame and ping goes through
// this one goroutine.
func (c *WSConn) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case frame := <-c.outbound:
			if err := c.writeEnvelope(frame.envelope); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		}
	}
}

func (c *WSConn) writeEnvelope(envelope WSEnvelope) error {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		log.Printf("control ws: encode outbound envelope: %v", err)
		return nil
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return c.conn.WriteMessage(websocket.TextMessage, encoded)
}

func mustMarshal(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// IsUnexpectedCloseError reports whether err represents an abnormal
// WebSocket closure, for callers that want to distinguish it from a clean
// disconnect.
func IsUnexpectedCloseError(err error) bool {
	if err == nil {
		return false
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code != websocket.CloseNormalClosure && closeErr.Code != websocket.CloseGoingAway
	}
	return true
}
