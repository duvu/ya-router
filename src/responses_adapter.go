// responses_adapter.go — Converts between OpenAI Chat Completions and
// Responses API formats.  The Codex device_code token (ChatGPT Plus) is
// authorised for /v1/responses but NOT /v1/chat/completions, so we proxy
// through the Responses API and translate on-the-fly.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Request conversion:  Chat Completions → Responses API
// ---------------------------------------------------------------------------

// chatToResponsesBody rewrites a Chat Completions JSON body into the
// equivalent Responses API body.
//
// Key mapping:
//
//	messages (system)    → instructions (joined string)
//	messages (non-system)→ input (array)
//	max_tokens           → max_output_tokens
//	n, stop              → dropped (unsupported in Responses API)
//	stream               → preserved
//	temperature          → preserved
//	top_p                → preserved
//	model                → preserved
func chatToResponsesBody(chatBody []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal chat body: %w", err)
	}

	out := make(map[string]json.RawMessage, len(raw)+2)

	for k, v := range raw {
		switch k {
		case "messages":
			// Split system messages → instructions; the rest → input array.
			var msgs []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(v, &msgs); err != nil {
				// Fallback: pass through as-is.
				out["input"] = v
				break
			}
			var instrParts []string
			var inputMsgs []map[string]json.RawMessage
			for _, m := range msgs {
				if m.Role == "system" {
					var text string
					if json.Unmarshal(m.Content, &text) == nil {
						instrParts = append(instrParts, text)
					}
				} else {
					roleJSON, _ := json.Marshal(m.Role)
					inputMsgs = append(inputMsgs, map[string]json.RawMessage{
						"role":    roleJSON,
						"content": m.Content,
					})
				}
			}
			instrJSON, _ := json.Marshal(strings.Join(instrParts, "\n"))
			out["instructions"] = instrJSON
			if inputMsgs == nil {
				inputMsgs = []map[string]json.RawMessage{}
			}
			inputJSON, _ := json.Marshal(inputMsgs)
			out["input"] = inputJSON
		case "max_tokens", "max_completion_tokens":
			out["max_output_tokens"] = v
		case "n", "stop", "logprobs", "top_logprobs", "logit_bias",
			"frequency_penalty", "presence_penalty", "seed",
			"response_format", "tools", "tool_choice",
			"parallel_tool_calls", "function_call", "functions":
			// Drop fields unsupported by Responses API.
		default:
			out[k] = v
		}
	}

	// Ensure instructions is always present (required by chatgpt.com endpoint).
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}

	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// Non-streaming response conversion:  Responses API → Chat Completions
// ---------------------------------------------------------------------------

// responsesAPIOutput is a single output item from the Responses API.
type responsesAPIOutput struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// responsesAPIResult is the top-level Responses API JSON body.
type responsesAPIResult struct {
	ID        string               `json:"id"`
	Object    string               `json:"object"`
	CreatedAt float64              `json:"created_at"`
	Model     string               `json:"model"`
	Output    []responsesAPIOutput `json:"output"`
	Usage     *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// responsesToChatCompletion converts a non-streaming Responses API body
// into the Chat Completions format expected by proxy clients.
func responsesToChatCompletion(respBody []byte) ([]byte, error) {
	var rr responsesAPIResult
	if err := json.Unmarshal(respBody, &rr); err != nil {
		return nil, fmt.Errorf("unmarshal responses body: %w", err)
	}

	// If the Responses API itself returned an error object, pass through.
	if rr.Error != nil {
		errResp := map[string]interface{}{
			"error": map[string]interface{}{
				"message": rr.Error.Message,
				"type":    rr.Error.Type,
				"code":    rr.Error.Code,
			},
		}
		return json.Marshal(errResp)
	}

	// Extract assistant text from output items.
	var text strings.Builder
	for _, item := range rr.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" {
				text.WriteString(c.Text)
			}
		}
	}

	cc := map[string]interface{}{
		"id":      rr.ID,
		"object":  "chat.completion",
		"created": int64(rr.CreatedAt),
		"model":   rr.Model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": text.String(),
				},
				"finish_reason": "stop",
			},
		},
	}
	if rr.Usage != nil {
		cc["usage"] = map[string]int{
			"prompt_tokens":     rr.Usage.InputTokens,
			"completion_tokens": rr.Usage.OutputTokens,
			"total_tokens":      rr.Usage.TotalTokens,
		}
	}
	return json.Marshal(cc)
}

// ---------------------------------------------------------------------------
// Streaming conversion:  Responses API SSE → Chat Completions SSE
// ---------------------------------------------------------------------------

// streamResponsesAsChat reads a Responses API SSE stream and writes
// Chat Completions–compatible SSE chunks to w.
func streamResponsesAsChat(w http.ResponseWriter, resp *http.Response) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("ResponseWriter does not support Flusher")
	}

	// Set headers for SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var (
		chatID    = fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
		model     string
		created   = time.Now().Unix()
		sentRole  bool
		eventType string
	)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE event type line.
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		// SSE data line.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "response.created":
			// Extract model from the response.created event.
			var ev struct {
				Response struct {
					ID    string `json:"id"`
					Model string `json:"model"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				if ev.Response.Model != "" {
					model = ev.Response.Model
				}
				if ev.Response.ID != "" {
					chatID = ev.Response.ID
				}
			}

		case "response.output_text.delta":
			var ev struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				log.Printf("[responses_adapter] cannot parse text delta: %v", err)
				continue
			}

			// First chunk: send role.
			if !sentRole {
				sentRole = true
				chunk := chatChunk(chatID, model, created, map[string]string{"role": "assistant"}, nil)
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				flusher.Flush()
			}

			chunk := chatChunk(chatID, model, created, map[string]string{"content": ev.Delta}, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.completed":
			// Extract usage from the completed event.
			var ev struct {
				Response struct {
					Usage *struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
						TotalTokens  int `json:"total_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			var usage map[string]int
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Response.Usage != nil {
				usage = map[string]int{
					"prompt_tokens":     ev.Response.Usage.InputTokens,
					"completion_tokens": ev.Response.Usage.OutputTokens,
					"total_tokens":      ev.Response.Usage.TotalTokens,
				}
			}
			// Send finish_reason chunk.
			finish := "stop"
			chunk := chatChunkFinish(chatID, model, created, &finish, usage)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

			// Send [DONE].
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil

		case "error":
			// Forward error as an SSE error event.
			log.Printf("[responses_adapter] upstream error event: %s", data)
			// Convert to chat completion error chunk.
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading responses SSE stream: %w", err)
	}
	return nil
}

// chatChunk builds a Chat Completions streaming chunk JSON.
func chatChunk(id, model string, created int64, delta map[string]string, finishReason *string) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// chatChunkFinish builds the final streaming chunk with finish_reason and optional usage.
func chatChunkFinish(id, model string, created int64, finishReason *string, usage map[string]int) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         map[string]string{},
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, _ := json.Marshal(chunk)
	return b
}

// isStreamingRequest checks if the request body has "stream":true.
func isStreamingRequest(body []byte) bool {
	var req struct {
		Stream interface{} `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	switch v := req.Stream.(type) {
	case bool:
		return v
	default:
		return false
	}
}

// patchBodyForChatGPT adjusts a Responses API body for chatgpt.com/backend-api/codex/responses:
//   - forces "stream": true  (endpoint requires it)
//   - forces "store": false  (endpoint requires it)
//   - removes "max_output_tokens" (unsupported by this endpoint)
func patchBodyForChatGPT(body []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	m["stream"], _ = json.Marshal(true)
	m["store"], _ = json.Marshal(false)
	delete(m, "max_output_tokens")
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

// aggregateSSEToCompletion reads a Responses API SSE stream and returns
// the response JSON from the response.completed event, suitable for
// passing to responsesToChatCompletion.
func aggregateSSEToCompletion(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "response.completed":
			var ev struct {
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return nil, fmt.Errorf("parse response.completed: %w", err)
			}
			return ev.Response, nil
		case "error":
			return nil, fmt.Errorf("upstream SSE error: %s", data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading responses SSE: %w", err)
	}
	return nil, fmt.Errorf("no response.completed event in SSE stream")
}

// handleResponsesAPIResponse processes a Responses API HTTP response and
// writes the translated Chat Completions output to w.
// clientWantsStream indicates whether the original client request asked
// for streaming; when forcing stream=true upstream (chatgpt.com mode) but
// the client wants a blocking response, we aggregate the SSE here.
func handleResponsesAPIResponse(w http.ResponseWriter, resp *http.Response, clientWantsStream bool) error {
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(ct, "text/event-stream")

	if resp.StatusCode >= 400 {
		// Error: read body and forward as-is, but also return an error
		// so the proxy layer can log the real upstream status.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[codex] upstream %d response: %s", resp.StatusCode, string(body))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}

	if isSSE {
		if !clientWantsStream {
			// Client requested a blocking response but upstream was forced to
			// stream (chatgpt.com mode).  Aggregate the SSE into a single
			// Chat Completions response object.
			respJSON, err := aggregateSSEToCompletion(resp.Body)
			if err != nil {
				return fmt.Errorf("aggregate SSE: %w", err)
			}
			chatBody, err := responsesToChatCompletion(respJSON)
			if err != nil {
				log.Printf("[responses_adapter] SSE aggregate conversion failed: %v — forwarding raw", err)
				chatBody = respJSON
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.WriteHeader(http.StatusOK)
			_, writeErr := io.Copy(w, bytes.NewReader(chatBody))
			return writeErr
		}
		return streamResponsesAsChat(w, resp)
	}

	// Non-streaming: read full body, convert, write.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading responses body: %w", err)
	}

	chatBody, err := responsesToChatCompletion(body)
	if err != nil {
		log.Printf("[responses_adapter] conversion failed: %v — forwarding raw", err)
		chatBody = body
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(resp.StatusCode)
	_, writeErr := io.Copy(w, bytes.NewReader(chatBody))
	return writeErr
}
