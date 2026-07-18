// ws_chat_handler.go implements control.WSHandler's chat methods by driving
// the existing /v1/chat/completions dispatch path (processProxyRequest)
// instead of a second router or duplicated provider logic (issue #76). One
// chat.start becomes one synthetic POST /v1/chat/completions request; the
// WS connection's chat.route/chat.delta/chat.done/chat.error events mirror
// exactly what an HTTP client would have received.
package yarouter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	controlpkg "github.com/duvu/ya-router/internal/control"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

// wsChatHandler adapts control.WSHandler's chat methods onto the daemon's
// existing runtime manager. It holds no provider/router state of its own —
// every chat.start acquires a fresh lease so it always dispatches through
// whatever snapshot (providers/router/config) is currently live, exactly
// like an ordinary HTTP request.
type wsChatHandler struct {
	runtime *runtimepkg.Manager
}

func newWSChatHandler(runtime *runtimepkg.Manager) *wsChatHandler {
	return &wsChatHandler{runtime: runtime}
}

func (h *wsChatHandler) HandleSnapshotRequest(context.Context, *controlpkg.WSConn) {
	// State delivery is StateAwareWSHandler's responsibility (see #75); this
	// handler is composed underneath it and never receives snapshot.request
	// directly (StateAwareWSHandler intercepts it before delegating).
}

func (h *wsChatHandler) HandleChatCancel(*controlpkg.WSConn) {
	// WSConn.cancelChat (see internal/control/ws_server.go) already cancels
	// the context passed to HandleChatStart; processProxyRequest observes
	// that cancellation through ctx exactly like an HTTP client disconnect,
	// so there is nothing additional to do here.
}

// HandleChatStart runs one chat request to completion (or cancellation) on
// the calling goroutine. WSConn.startChat (see ws_server.go) already runs
// this on its own per-chat goroutine, so a slow/long chat blocks neither the
// connection's next message nor the data plane.
func (h *wsChatHandler) HandleChatStart(ctx context.Context, conn *controlpkg.WSConn, requestID string, payload controlpkg.WSChatStartPayload) {
	lease, err := h.runtime.Acquire()
	if err != nil {
		conn.Send(controlpkg.WSTypeChatError, requestID, mustMarshalChatError("provider_unavailable", "the daemon is shutting down"))
		return
	}
	defer lease.Release()
	snapshot := lease.Snapshot()

	body, err := buildWSChatRequestBody(payload)
	if err != nil {
		conn.Send(controlpkg.WSTypeChatError, requestID, mustMarshalChatError("invalid_request", "chat.start payload is malformed"))
		return
	}

	routedOnce := false
	observer := func(providerID ProviderID, resolvedModel string) {
		if routedOnce {
			return
		}
		routedOnce = true
		conn.Send(controlpkg.WSTypeChatRoute, requestID, mustMarshalChatRoute(string(providerID), resolvedModel))
	}

	chatWriter := newWSChatWriter(func(text string) {
		conn.Send(controlpkg.WSTypeChatDelta, requestID, mustMarshalChatDelta(text))
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(bytes.NewReader(body)))
	request.ContentLength = int64(len(body))
	observedCtx := withRouteObserver(ctx, observer)

	proxyErr := processProxyRequest(snapshot.Providers(), snapshot.Router(), snapshot.Config(), chatWriter, request, observedCtx)
	chatWriter.Finish()

	if ctx.Err() != nil {
		conn.Send(controlpkg.WSTypeChatError, requestID, mustMarshalChatError("canceled", "chat was canceled"))
		return
	}
	if proxyErr != nil || chatWriter.StatusCode() >= http.StatusBadRequest {
		category := classifyErrorCategory(chatWriter.StatusCode(), proxyErr)
		conn.Send(controlpkg.WSTypeChatError, requestID, mustMarshalChatError(string(category), chatErrorCategoryMessage(category)))
		return
	}
	conn.Send(controlpkg.WSTypeChatDone, requestID, mustMarshalChatDone("stop"))
}

// buildWSChatRequestBody assembles a standard streaming chat/completions
// request body from the chat.start payload. Model defaults to empty (the
// router's configured default, i.e. thiendu) when omitted, matching
// ordinary HTTP requests that omit "model".
func buildWSChatRequestBody(payload controlpkg.WSChatStartPayload) ([]byte, error) {
	if len(payload.Messages) == 0 {
		return nil, errWSChatMessagesRequired
	}
	request := map[string]any{
		"messages": json.RawMessage(payload.Messages),
		"stream":   true,
	}
	if strings.TrimSpace(payload.Model) != "" {
		request["model"] = payload.Model
	}
	return json.Marshal(request)
}

var errWSChatMessagesRequired = &wsChatValidationError{"chat.start requires at least one message"}

type wsChatValidationError struct{ msg string }

func (e *wsChatValidationError) Error() string { return e.msg }

func mustMarshalChatRoute(providerID, resolvedModel string) json.RawMessage {
	encoded, err := json.Marshal(controlpkg.WSChatRoutePayload{Provider: providerID, ResolvedModel: resolvedModel})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func mustMarshalChatDelta(text string) json.RawMessage {
	encoded, err := json.Marshal(controlpkg.WSChatDeltaPayload{Text: text})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func mustMarshalChatDone(finishReason string) json.RawMessage {
	encoded, err := json.Marshal(controlpkg.WSChatDonePayload{FinishReason: finishReason})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func mustMarshalChatError(category, message string) json.RawMessage {
	encoded, err := json.Marshal(controlpkg.WSChatErrorPayload{Category: category, Message: message})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// chatErrorCategoryMessage maps a bounded telemetry.ErrorCategory to a
// bounded, human-readable description. It never surfaces a raw upstream
// error string — only the stable category-derived text.
func chatErrorCategoryMessage(category telemetrypkg.ErrorCategory) string {
	switch category {
	case telemetrypkg.ErrorCategoryModelUnavailable:
		return "no active target is available for this model"
	case telemetrypkg.ErrorCategoryAuthRequired:
		return "the selected provider requires authentication"
	case telemetrypkg.ErrorCategoryRateLimited:
		return "the selected provider is rate-limited"
	case telemetrypkg.ErrorCategoryProviderUnavailable:
		return "the selected provider is temporarily unavailable"
	case telemetrypkg.ErrorCategoryTimeout:
		return "the request timed out"
	case telemetrypkg.ErrorCategoryEntitlementDenied:
		return "the selected provider denied this request"
	case telemetrypkg.ErrorCategoryUnsupported:
		return "this capability is not supported by the selected provider"
	case telemetrypkg.ErrorCategoryInvalidRequest:
		return "the chat request was invalid"
	default:
		return "chat request failed"
	}
}
