// ws_chat_writer.go adapts the existing /v1/chat/completions streaming
// response shape into ordered chat.delta events for a WS connection. It is
// an http.ResponseWriter so it can be handed straight to
// processProxyRequest — the same dispatch path an ordinary HTTP client uses
// — without a second router or duplicated provider logic.
package yarouter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// wsChatWriter buffers SSE "data: " lines, extracts assistant text deltas in
// order, and forwards each one to onDelta. It never writes anything back to
// a real network connection: it exists purely to observe the same bytes an
// HTTP client would have received.
type wsChatWriter struct {
	header    http.Header
	status    int
	pending   bytes.Buffer
	onDelta   func(text string)
	sawStream bool
	nonStream bytes.Buffer
}

func newWSChatWriter(onDelta func(text string)) *wsChatWriter {
	return &wsChatWriter{header: make(http.Header), onDelta: onDelta}
}

func (w *wsChatWriter) Header() http.Header { return w.header }

func (w *wsChatWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *wsChatWriter) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *wsChatWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		w.sawStream = true
		w.pending.Write(data)
		w.drainSSELines()
		return len(data), nil
	}
	w.nonStream.Write(data)
	return len(data), nil
}

func (w *wsChatWriter) Flush() {}

// Finish extracts the terminal assistant text for a non-streaming response.
// It is a no-op (returns "") when the response was SSE, since deltas were
// already forwarded incrementally via onDelta.
func (w *wsChatWriter) Finish() string {
	if w.sawStream || w.nonStream.Len() == 0 {
		return ""
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(w.nonStream.Bytes(), &response); err != nil || len(response.Choices) == 0 {
		return ""
	}
	text := response.Choices[0].Message.Content
	if text != "" && w.onDelta != nil {
		w.onDelta(text)
	}
	return text
}

func (w *wsChatWriter) drainSSELines() {
	for {
		data := w.pending.Bytes()
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			return
		}
		line := append([]byte(nil), data[:newline]...)
		w.pending.Next(newline + 1)
		w.handleSSELine(bytes.TrimRight(line, "\r"))
	}
}

func (w *wsChatWriter) handleSSELine(line []byte) {
	const prefix = "data: "
	trimmed := strings.TrimPrefix(string(line), prefix)
	if trimmed == string(line) || trimmed == "" || trimmed == "[DONE]" {
		return
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(trimmed), &chunk) != nil {
		return
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" && w.onDelta != nil {
			w.onDelta(choice.Delta.Content)
		}
	}
}
