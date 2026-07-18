package control

import (
	"context"
	"encoding/json"
	"log"
)

// StateAwareWSHandler answers snapshot.request (and the automatic
// post-connect snapshot) from a StateReader and delegates every chat message
// to an inner handler. This lets the snapshot/state feature (issue #75) land
// independently of the chat feature (issue #76): today Chat is typically
// NoopWSHandler, and #76 replaces it without touching state delivery.
type StateAwareWSHandler struct {
	Reader StateReader
	Chat   WSHandler
}

func (h StateAwareWSHandler) HandleSnapshotRequest(ctx context.Context, conn *WSConn) {
	if h.Reader == nil {
		conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
		return
	}
	payload, err := h.Reader.State(ctx)
	if err != nil {
		log.Printf("control ws: snapshot read failed: %v", err)
		conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("control ws: encode snapshot: %v", err)
		conn.Send(WSTypeSnapshot, "", json.RawMessage(`{}`))
		return
	}
	conn.Send(WSTypeSnapshot, "", encoded)
}

func (h StateAwareWSHandler) HandleChatStart(ctx context.Context, conn *WSConn, requestID string, payload WSChatStartPayload) {
	if h.Chat == nil {
		conn.Send(WSTypeChatError, requestID, mustMarshal(WSChatErrorPayload{Category: "unsupported_capability", Message: "chat is not yet available"}))
		return
	}
	h.Chat.HandleChatStart(ctx, conn, requestID, payload)
}

func (h StateAwareWSHandler) HandleChatCancel(conn *WSConn) {
	if h.Chat == nil {
		return
	}
	h.Chat.HandleChatCancel(conn)
}
