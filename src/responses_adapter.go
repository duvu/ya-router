// responses_adapter.go — Converts between OpenAI Chat Completions and
// Responses API formats.  The Codex device_code token (ChatGPT Plus) is
// authorised for chatgpt.com/backend-api/codex/responses (ChatGPT mode) or
// api.openai.com/v1/responses (api_key mode).  Two transport-specific
// request builders produce the correct body for each backend:
//
//   - buildChatGPTCodexRequest  — strict allowlist, forces stream/store
//   - buildPlatformResponsesRequest — generic conversion, drop-list based
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
// Request conversion helpers
// ---------------------------------------------------------------------------

// streamOptionsIncludeUsage returns true if stream_options.include_usage is
// set in the raw field map.  The field is a Chat Completions concept and must
// never be forwarded to upstream Responses API endpoints.
func streamOptionsIncludeUsage(raw map[string]json.RawMessage) bool {
	v, ok := raw["stream_options"]
	if !ok {
		return false
	}
	var so struct {
		IncludeUsage bool `json:"include_usage"`
	}
	return json.Unmarshal(v, &so) == nil && so.IncludeUsage
}

// normalizeContentParts rewrites Chat Completions content-part type names to
// the Responses API equivalents so the upstream never receives an unsupported
// "text" or "image_url" type value.
//
// Chat Completions → Responses API mapping:
//
//	"text"      → "input_text"
//	"image_url" → "input_image"
//
// Plain string content is returned unchanged.  Unknown part types are passed
// through without modification to preserve forward-compatibility.
func normalizeContentParts(content json.RawMessage) json.RawMessage {
	// Plain string content: nothing to rewrite.
	var s string
	if json.Unmarshal(content, &s) == nil {
		return content
	}
	// Array of content parts: rewrite Chat Completions type names.
	var parts []map[string]json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return content
	}
	for i, part := range parts {
		typeRaw, ok := part["type"]
		if !ok {
			continue
		}
		var t string
		if json.Unmarshal(typeRaw, &t) != nil {
			continue
		}
		switch t {
		case "text":
			parts[i]["type"], _ = json.Marshal("input_text")
		case "image_url":
			parts[i]["type"], _ = json.Marshal("input_image")
		}
	}
	out, _ := json.Marshal(parts)
	return out
}

// convertToolsForResponses rewrites the Chat Completions "tools" array into the
// Responses API format.  In Chat Completions each tool wraps its definition
// inside a "function" object; the Responses API flattens it:
//
//	Chat Completions: {"type":"function","function":{"name":"f",…}}
//	Responses API:    {"type":"function","name":"f",…}
func convertToolsForResponses(raw json.RawMessage) json.RawMessage {
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description,omitempty"`
			Parameters  json.RawMessage `json:"parameters,omitempty"`
			Strict      *bool           `json:"strict,omitempty"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return raw
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		rt := map[string]interface{}{
			"type": "function",
			"name": t.Function.Name,
		}
		if t.Function.Description != "" {
			rt["description"] = t.Function.Description
		}
		if t.Function.Parameters != nil {
			rt["parameters"] = t.Function.Parameters
		}
		if t.Function.Strict != nil {
			rt["strict"] = *t.Function.Strict
		}
		out = append(out, rt)
	}
	b, _ := json.Marshal(out)
	return b
}

// extractMessages splits the Chat Completions "messages" array into:
//   - instructions: system-role content joined with newlines
//   - inputJSON:    remaining messages as a JSON array, with content-part
//     types rewritten for Responses API compatibility (text→input_text, etc.)
func extractMessages(v json.RawMessage) (instructions string, inputJSON json.RawMessage, err error) {
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err = json.Unmarshal(v, &msgs); err != nil {
		return "", v, fmt.Errorf("parse messages: %w", err)
	}
	var instrParts []string
	var inputMsgs []map[string]json.RawMessage
	for _, m := range msgs {
		if m.Role == "system" {
			// content may be a plain string or an array of parts.
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				instrParts = append(instrParts, text)
			} else {
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}
				if json.Unmarshal(m.Content, &parts) == nil {
					for _, p := range parts {
						if p.Text != "" {
							instrParts = append(instrParts, p.Text)
						}
					}
				}
			}
		} else {
			roleJSON, _ := json.Marshal(m.Role)
			inputMsgs = append(inputMsgs, map[string]json.RawMessage{
				"role":    roleJSON,
				"content": normalizeContentParts(m.Content),
			})
		}
	}
	if inputMsgs == nil {
		inputMsgs = []map[string]json.RawMessage{}
	}
	result, _ := json.Marshal(inputMsgs)
	return strings.Join(instrParts, "\n"), result, nil
}

// ---------------------------------------------------------------------------
// Request conversion:  Chat Completions → transport-specific Responses body
// ---------------------------------------------------------------------------

// chatGPTCodexAllowedKeys is the strict allowlist of fields accepted by
// chatgpt.com/backend-api/codex/responses.  Anything not in this set is
// silently dropped before the request leaves the proxy.
var chatGPTCodexAllowedKeys = map[string]bool{
	"model":        true,
	"input":        true,
	"instructions": true,
	"tools":        true,
	"stream":       true,
	"store":        true,
	"temperature":  true,
	"top_p":        true,
	"user":         true,
}

// buildChatGPTCodexRequest converts an OpenAI Chat Completions body into the
// request format required by chatgpt.com/backend-api/codex/responses.
//
// Differences from the Platform Responses API:
//   - strict allowlist: only chatGPTCodexAllowedKeys are forwarded
//   - stream is always forced to true  (endpoint requirement)
//   - store  is always forced to false (endpoint requirement)
//   - stream_options is consumed locally; include_usage is returned
//   - max_tokens/max_output_tokens are dropped (unsupported)
//
// Returns the serialised request body and whether the client requested usage
// in streaming chunks (stream_options.include_usage).
func buildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)

	out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))

	// Extract messages → instructions + input.
	if v, ok := raw["messages"]; ok {
		instr, inputJSON, err := extractMessages(v)
		if err != nil {
			out["input"] = v // fallback
		} else {
			instrJSON, _ := json.Marshal(instr)
			out["instructions"] = instrJSON
			out["input"] = inputJSON
		}
	}
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}

	// Safe pass-through fields from the allowlist (excluding messages already handled).
	for _, k := range []string{"model", "temperature", "top_p", "user"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}

	// Convert Chat Completions tools format to Responses API format.
	if v, ok := raw["tools"]; ok {
		out["tools"] = convertToolsForResponses(v)
	}

	// Endpoint requirements.
	out["stream"], _ = json.Marshal(true)
	out["store"], _ = json.Marshal(false)

	b, err := json.Marshal(out)
	return b, includeUsage, err
}

// buildPlatformResponsesRequest converts an OpenAI Chat Completions body into
// the generic Responses API format for api.openai.com/v1/responses.
//
//   - messages (system)     → instructions
//   - messages (non-system) → input
//   - max_tokens            → max_output_tokens
//   - stream_options        → consumed locally; include_usage returned
//   - n, stop, etc.         → dropped
//   - all other fields      → passed through
func buildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)

	out := make(map[string]json.RawMessage, len(raw)+2)

	for k, v := range raw {
		switch k {
		case "messages":
			instr, inputJSON, err := extractMessages(v)
			if err != nil {
				out["input"] = v // fallback
			} else {
				instrJSON, _ := json.Marshal(instr)
				out["instructions"] = instrJSON
				out["input"] = inputJSON
			}
		case "max_tokens", "max_completion_tokens":
			out["max_output_tokens"] = v
		case "stream_options":
			// Consumed locally — never forwarded upstream.
		case "tools":
			out["tools"] = convertToolsForResponses(v)
		case "n", "stop", "logprobs", "top_logprobs", "logit_bias",
			"frequency_penalty", "presence_penalty", "seed",
			"response_format", "tool_choice",
			"parallel_tool_calls", "function_call", "functions":
			// Drop fields unsupported by Responses API.
		default:
			out[k] = v
		}
	}

	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}

	b, err := json.Marshal(out)
	return b, includeUsage, err
}

// ---------------------------------------------------------------------------
// Non-streaming response conversion:  Responses API → Chat Completions
// ---------------------------------------------------------------------------

// responsesAPIOutput is a single output item from the Responses API.
type responsesAPIOutput struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Role      string `json:"role"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   []struct {
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

	// Extract assistant text and tool calls from output items.
	var text strings.Builder
	var toolCalls []map[string]interface{}
	for _, item := range rr.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   item.CallID,
				"type": "function",
				"function": map[string]string{
					"name":      item.Name,
					"arguments": item.Arguments,
				},
			})
		}
	}

	finishReason := "stop"
	msg := map[string]interface{}{
		"role":    "assistant",
		"content": text.String(),
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		msg["tool_calls"] = toolCalls
	}

	cc := map[string]interface{}{
		"id":      rr.ID,
		"object":  "chat.completion",
		"created": int64(rr.CreatedAt),
		"model":   rr.Model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
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
// includeUsage controls whether usage is appended to the final chunk;
// it reflects stream_options.include_usage from the original client request.
func streamResponsesAsChat(w http.ResponseWriter, resp *http.Response, includeUsage bool) error {
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
		chatID          = fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
		model           string
		created         = time.Now().Unix()
		sentRole        bool
		eventType       string
		hadToolCalls    bool
		toolCallCount   int
		toolCallIndices = make(map[string]int) // call_id → index
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

		case "response.output_item.added":
			var ev struct {
				Item struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Name   string `json:"name"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil || ev.Item.Type != "function_call" {
				continue
			}
			hadToolCalls = true
			idx := toolCallCount
			toolCallCount++
			toolCallIndices[ev.Item.CallID] = idx

			// Build delta with role (on first chunk) + tool_calls.
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{
					{
						"index": idx,
						"id":    ev.Item.CallID,
						"type":  "function",
						"function": map[string]string{
							"name":      ev.Item.Name,
							"arguments": "",
						},
					},
				},
			}
			if !sentRole {
				sentRole = true
				delta["role"] = "assistant"
			}
			chunk := chatChunkDynamic(chatID, model, created, delta, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.function_call_arguments.delta":
			var ev struct {
				Delta  string `json:"delta"`
				CallID string `json:"call_id"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			idx, ok := toolCallIndices[ev.CallID]
			if !ok {
				continue
			}
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{
					{
						"index": idx,
						"function": map[string]string{
							"arguments": ev.Delta,
						},
					},
				},
			}
			chunk := chatChunkDynamic(chatID, model, created, delta, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.completed":
			// Extract usage from the completed event; only include it in the
			// final chunk when the client requested it via
			// stream_options.include_usage.
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
			if includeUsage && json.Unmarshal([]byte(data), &ev) == nil && ev.Response.Usage != nil {
				usage = map[string]int{
					"prompt_tokens":     ev.Response.Usage.InputTokens,
					"completion_tokens": ev.Response.Usage.OutputTokens,
					"total_tokens":      ev.Response.Usage.TotalTokens,
				}
			}
			// Send finish_reason chunk.
			finish := "stop"
			if hadToolCalls {
				finish = "tool_calls"
			}
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

// chatChunkDynamic builds a streaming chunk with an arbitrary delta object
// (used for tool_calls and mixed deltas that contain non-string fields).
func chatChunkDynamic(id, model string, created int64, delta map[string]interface{}, finishReason *string) []byte {
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
//
//   - clientWantsStream: true when the original client requested SSE streaming
//   - upstreamSSE: true when the upstream was forced to stream (chatgpt.com
//     mode); the endpoint may not set Content-Type: text/event-stream
//   - includeUsage: true when the client requested usage via
//     stream_options.include_usage (streaming only)
func handleResponsesAPIResponse(w http.ResponseWriter, resp *http.Response, clientWantsStream, upstreamSSE, includeUsage bool) error {
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(ct, "text/event-stream") || upstreamSSE

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
		return streamResponsesAsChat(w, resp, includeUsage)
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
