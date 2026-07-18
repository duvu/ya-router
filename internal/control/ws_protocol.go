package control

import "encoding/json"

// WSProtocolVersion is the ya-control WebSocket subprotocol version this
// daemon speaks. It is sent in the "hello" message so a client can detect an
// incompatible daemon before sending chat.start.
const WSProtocolVersion = 1

// WSMessageType is the closed set of message type strings carried over
// /control/v1/ws. Both directions share one envelope shape; MessageType
// determines how Payload is interpreted.
type WSMessageType string

const (
	// Client -> daemon.
	WSTypeChatStart       WSMessageType = "chat.start"
	WSTypeChatCancel      WSMessageType = "chat.cancel"
	WSTypeSnapshotRequest WSMessageType = "snapshot.request"
	WSTypePing            WSMessageType = "ping"

	// Daemon -> client.
	WSTypeHello        WSMessageType = "hello"
	WSTypeSnapshot     WSMessageType = "snapshot"
	WSTypeStateUpdated WSMessageType = "state.updated"
	WSTypeChatRoute    WSMessageType = "chat.route"
	WSTypeChatDelta    WSMessageType = "chat.delta"
	WSTypeChatDone     WSMessageType = "chat.done"
	WSTypeChatError    WSMessageType = "chat.error"
	WSTypePong         WSMessageType = "pong"
)

// WSEnvelope is the one typed shape every /control/v1/ws frame uses in both
// directions. RequestID is optional and, when set on a client message, is
// echoed back on every daemon message caused by that request (chat.route,
// chat.delta, chat.done/chat.error) so a client can correlate them. Sequence
// is set by the daemon on every daemon->client message and is monotonic per
// connection, letting a client detect and ignore duplicate/out-of-order
// delivery; it is always 0 on client->daemon messages.
type WSEnvelope struct {
	Type      WSMessageType   `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Sequence  uint64          `json:"sequence,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// WSHelloPayload is the first message sent on every new connection.
type WSHelloPayload struct {
	ProtocolVersion int    `json:"protocol_version"`
	ServiceVersion  string `json:"service_version"`
	ConfigRevision  uint64 `json:"config_revision"`
}

// WSChatStartPayload starts one chat request over the connection. Model
// defaults to the daemon's configured default (thiendu) when omitted,
// matching ordinary HTTP chat requests.
type WSChatStartPayload struct {
	Model    string          `json:"model,omitempty"`
	Messages json.RawMessage `json:"messages"`
}

// WSChatRoutePayload reports the provider/model selected for a chat.start
// once routing has resolved it, before any delta is sent.
type WSChatRoutePayload struct {
	Provider      string `json:"provider"`
	ResolvedModel string `json:"resolved_model"`
}

// WSChatDeltaPayload carries one ordered increment of assistant text.
type WSChatDeltaPayload struct {
	Text string `json:"text"`
}

// WSChatDonePayload is the terminal success event for one chat.start.
type WSChatDonePayload struct {
	FinishReason string `json:"finish_reason,omitempty"`
}

// WSChatErrorPayload is the terminal error event for one chat.start. Message
// is a bounded, redacted description; it never contains raw upstream error
// bodies, prompts, or completions.
type WSChatErrorPayload struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// WSErrorCode is the closed set of protocol-level error codes, distinct from
// chat-outcome error categories.
type WSErrorCode string

const (
	WSErrorMalformedMessage WSErrorCode = "malformed_message"
	WSErrorMessageTooLarge  WSErrorCode = "message_too_large"
	WSErrorUnknownType      WSErrorCode = "unknown_message_type"
	WSErrorChatInProgress   WSErrorCode = "chat_already_in_progress"
	WSErrorNoActiveChat     WSErrorCode = "no_active_chat"
)

// WSProtocolErrorPayload reports a malformed/oversized/unsupported client
// message. It is sent as a chat.error-shaped envelope only when a
// request_id is available; otherwise the connection is closed per the
// "malformed and oversized messages fail safely" requirement.
type WSProtocolErrorPayload struct {
	Code    WSErrorCode `json:"code"`
	Message string      `json:"message"`
}
