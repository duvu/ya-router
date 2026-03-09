// responses_adapter_test.go — Tests for Chat Completions ↔ Responses API adapter.
package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// normalizeContentParts
// ---------------------------------------------------------------------------

func TestNormalizeContentParts_PlainString(t *testing.T) {
	in := json.RawMessage(`"Hello world"`)
	out := normalizeContentParts(in)
	if string(out) != `"Hello world"` {
		t.Errorf("plain string should pass through unchanged, got %s", out)
	}
}

func TestNormalizeContentParts_TextToInputText(t *testing.T) {
	in := json.RawMessage(`[{"type":"text","text":"Hello"}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(out, &parts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("type = %q, want input_text", typ)
	}
	var text string
	json.Unmarshal(parts[0]["text"], &text)
	if text != "Hello" {
		t.Errorf("text = %q, want Hello", text)
	}
}

func TestNormalizeContentParts_ImageUrlToInputImage(t *testing.T) {
	in := json.RawMessage(`[{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)

	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_image" {
		t.Errorf("type = %q, want input_image", typ)
	}
	// image_url object should still be preserved
	if _, ok := parts[0]["image_url"]; !ok {
		t.Error("image_url key should be preserved in output")
	}
}

func TestNormalizeContentParts_MixedParts(t *testing.T) {
	in := json.RawMessage(`[{"type":"text","text":"Say"},{"type":"image_url","image_url":{"url":"u"}}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	var t0, t1 string
	json.Unmarshal(parts[0]["type"], &t0)
	json.Unmarshal(parts[1]["type"], &t1)
	if t0 != "input_text" {
		t.Errorf("parts[0].type = %q, want input_text", t0)
	}
	if t1 != "input_image" {
		t.Errorf("parts[1].type = %q, want input_image", t1)
	}
}

func TestNormalizeContentParts_UnknownTypePassesThrough(t *testing.T) {
	// Unknown types must pass through unchanged (forward-compatibility).
	in := json.RawMessage(`[{"type":"computer_screenshot","data":"base64stuff"}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "computer_screenshot" {
		t.Errorf("unknown type should pass through, got %q", typ)
	}
}

// ---------------------------------------------------------------------------
// extractMessages — array content
// ---------------------------------------------------------------------------

func TestExtractMessages_ArrayContentNormalized(t *testing.T) {
	msgs := json.RawMessage(`[
		{"role":"user","content":[{"type":"text","text":"Say hi"}]}
	]`)
	_, inputJSON, err := extractMessages(msgs)
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}

	var input []map[string]json.RawMessage
	json.Unmarshal(inputJSON, &input)
	if len(input) != 1 {
		t.Fatalf("input len = %d, want 1", len(input))
	}

	var content []map[string]json.RawMessage
	json.Unmarshal(input[0]["content"], &content)
	if len(content) != 1 {
		t.Fatalf("content parts len = %d, want 1", len(content))
	}
	var typ string
	json.Unmarshal(content[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("content[0].type = %q, want input_text (Chat Completions 'text' must be converted)", typ)
	}
}

func TestExtractMessages_SystemArrayContentExtracted(t *testing.T) {
	// System messages with array content: text must be extracted into instructions.
	msgs := json.RawMessage(`[
		{"role":"system","content":[{"type":"text","text":"Be concise."}]},
		{"role":"user","content":"Hello"}
	]`)
	instructions, inputJSON, err := extractMessages(msgs)
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}
	if instructions != "Be concise." {
		t.Errorf("instructions = %q, want 'Be concise.'", instructions)
	}
	var input []map[string]json.RawMessage
	json.Unmarshal(inputJSON, &input)
	if len(input) != 1 {
		t.Fatalf("input len = %d, want 1 (only user message)", len(input))
	}
}

// ---------------------------------------------------------------------------
// buildChatGPTCodexRequest — array content
// ---------------------------------------------------------------------------

func TestBuildChatGPTCodexRequest_ArrayContentNormalized(t *testing.T) {
	// Simulates the failing client that sends content as array parts.
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "Hello"}]}
		],
		"stream": true
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	var inputMsgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &inputMsgs)
	if len(inputMsgs) != 1 {
		t.Fatalf("input msgs = %d, want 1", len(inputMsgs))
	}
	var parts []map[string]json.RawMessage
	json.Unmarshal(inputMsgs[0]["content"], &parts)
	if len(parts) != 1 {
		t.Fatalf("content parts = %d, want 1", len(parts))
	}
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("content part type = %q, want input_text — 'text' must be rewritten to avoid OpenAIException upstream", typ)
	}
}

func TestBuildChatGPTCodexRequest_ImageUrlNormalized(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "Describe this"},
				{"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
			]}
		]
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	var msgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &msgs)
	var parts []map[string]json.RawMessage
	json.Unmarshal(msgs[0]["content"], &parts)

	var t0, t1 string
	json.Unmarshal(parts[0]["type"], &t0)
	json.Unmarshal(parts[1]["type"], &t1)
	if t0 != "input_text" {
		t.Errorf("parts[0].type = %q, want input_text", t0)
	}
	if t1 != "input_image" {
		t.Errorf("parts[1].type = %q, want input_image", t1)
	}
}

// ---------------------------------------------------------------------------
// buildPlatformResponsesRequest
// ---------------------------------------------------------------------------

func TestBuildPlatformResponsesRequest_Basic(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		],
		"max_tokens": 100,
		"temperature": 0.7,
		"stream": false
	}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if _, ok := m["messages"]; ok {
		t.Error("output contains 'messages' — should be split into input/instructions")
	}
	if _, ok := m["input"]; !ok {
		t.Error("output missing 'input'")
	}
	if _, ok := m["instructions"]; !ok {
		t.Error("output missing 'instructions'")
	}
	if _, ok := m["max_tokens"]; ok {
		t.Error("output contains 'max_tokens' — should be renamed to 'max_output_tokens'")
	}
	if _, ok := m["max_output_tokens"]; !ok {
		t.Error("output missing 'max_output_tokens'")
	}
	for _, key := range []string{"model", "temperature", "stream"} {
		if _, ok := m[key]; !ok {
			t.Errorf("output missing preserved key %q", key)
		}
	}
}

func TestBuildPlatformResponsesRequest_DropsUnsupported(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hi"}],
		"n": 2,
		"stop": ["\n"],
		"frequency_penalty": 0.5,
		"presence_penalty": 0.5
	}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	for _, key := range []string{"n", "stop", "frequency_penalty", "presence_penalty"} {
		if _, ok := m[key]; ok {
			t.Errorf("output should not contain dropped key %q", key)
		}
	}
}

func TestBuildPlatformResponsesRequest_MaxCompletionTokens(t *testing.T) {
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}],"max_completion_tokens":200}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	if _, ok := m["max_completion_tokens"]; ok {
		t.Error("output contains 'max_completion_tokens' — should be renamed")
	}
	if _, ok := m["max_output_tokens"]; !ok {
		t.Error("output missing 'max_output_tokens'")
	}
}

func TestBuildPlatformResponsesRequest_StreamOptions(t *testing.T) {
	// stream_options should not be forwarded; include_usage extracted.
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`

	out, includeUsage, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}
	if !includeUsage {
		t.Error("includeUsage should be true when stream_options.include_usage=true")
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if _, ok := m["stream_options"]; ok {
		t.Error("output must not contain 'stream_options' — it is a client-contract field")
	}
}

// ---------------------------------------------------------------------------
// buildChatGPTCodexRequest
// ---------------------------------------------------------------------------

func TestBuildChatGPTCodexRequest_AllowlistOnly(t *testing.T) {
	// All of these should be dropped by the allowlist.
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "Be helpful."},
			{"role": "user", "content": "Hello"}
		],
		"stream": false,
		"store": true,
		"max_tokens": 100,
		"max_output_tokens": 100,
		"n": 2,
		"stop": ["\n"],
		"logprobs": true,
		"stream_options": {"include_usage": true},
		"frequency_penalty": 0.5,
		"presence_penalty": 0.5,
		"seed": 42,
		"response_format": {"type": "json_object"},
		"tools": [],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"function_call": "auto",
		"functions": []
	}`

	out, includeUsage, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	if !includeUsage {
		t.Error("includeUsage should be true from stream_options.include_usage")
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	// Required fields.
	for _, k := range []string{"model", "input", "instructions", "stream", "store"} {
		if _, ok := m[k]; !ok {
			t.Errorf("output missing required field %q", k)
		}
	}
	// stream must be true, store must be false.
	var streamVal bool
	json.Unmarshal(m["stream"], &streamVal)
	if !streamVal {
		t.Error("stream must be forced to true in chatgpt mode")
	}
	var storeVal bool
	json.Unmarshal(m["store"], &storeVal)
	if storeVal {
		t.Error("store must be forced to false in chatgpt mode")
	}

	// None of these must appear.
	forbidden := []string{
		"max_tokens", "max_output_tokens", "max_completion_tokens",
		"n", "stop", "logprobs", "top_logprobs", "logit_bias",
		"stream_options", "frequency_penalty", "presence_penalty",
		"seed", "response_format", "tools", "tool_choice",
		"parallel_tool_calls", "function_call", "functions",
		"messages",
	}
	for _, k := range forbidden {
		if _, ok := m[k]; ok {
			t.Errorf("output must not contain %q — not in chatgpt.com allowlist", k)
		}
	}
}

func TestBuildChatGPTCodexRequest_SystemMessagesToInstructions(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "First instruction."},
			{"role": "system", "content": "Second instruction."},
			{"role": "user", "content": "Hello"}
		]
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	var instructions string
	json.Unmarshal(m["instructions"], &instructions)
	if instructions != "First instruction.\nSecond instruction." {
		t.Errorf("instructions = %q, want 'First instruction.\\nSecond instruction.'", instructions)
	}

	var inputMsgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &inputMsgs)
	if len(inputMsgs) != 1 {
		t.Errorf("input len = %d, want 1 (only user message)", len(inputMsgs))
	}
}

func TestBuildChatGPTCodexRequest_NoSystemMessages(t *testing.T) {
	// When there are no system messages, instructions must still be present as "".
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}]}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	instrRaw, ok := m["instructions"]
	if !ok {
		t.Fatal("output missing 'instructions' — required even when empty")
	}
	var instructions string
	json.Unmarshal(instrRaw, &instructions)
	if instructions != "" {
		t.Errorf("instructions = %q, want empty string", instructions)
	}
}

func TestBuildChatGPTCodexRequest_StreamOptionsNoUsage(t *testing.T) {
	// stream_options absent → includeUsage=false
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	_, includeUsage, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	if includeUsage {
		t.Error("includeUsage should be false when stream_options is absent")
	}
}

// ---------------------------------------------------------------------------
// streamOptionsIncludeUsage
// ---------------------------------------------------------------------------

func TestStreamOptionsIncludeUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"include_usage true", `{"stream_options":{"include_usage":true}}`, true},
		{"include_usage false", `{"stream_options":{"include_usage":false}}`, false},
		{"absent", `{"model":"gpt-5.4"}`, false},
		{"empty stream_options", `{"stream_options":{}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			json.Unmarshal([]byte(tt.body), &raw)
			got := streamOptionsIncludeUsage(raw)
			if got != tt.want {
				t.Errorf("streamOptionsIncludeUsage = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// responsesToChatCompletion
// ---------------------------------------------------------------------------

func TestResponsesToChatCompletion_Basic(t *testing.T) {
	respBody := `{
		"id": "resp_123",
		"object": "response",
		"created_at": 1709900000,
		"model": "gpt-5.4",
		"output": [
			{
				"type": "message",
				"id": "msg_1",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Hello! How can I help?"}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 8,
			"total_tokens": 18
		}
	}`

	out, err := responsesToChatCompletion([]byte(respBody))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}

	var cc struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &cc); err != nil {
		t.Fatalf("unmarshal chat completion: %v", err)
	}

	if cc.ID != "resp_123" {
		t.Errorf("id = %q, want resp_123", cc.ID)
	}
	if cc.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", cc.Object)
	}
	if cc.Model != "gpt-5.4" {
		t.Errorf("model = %q, want gpt-5.4", cc.Model)
	}
	if len(cc.Choices) != 1 {
		t.Fatalf("choices count = %d, want 1", len(cc.Choices))
	}
	if cc.Choices[0].Message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", cc.Choices[0].Message.Role)
	}
	if cc.Choices[0].Message.Content != "Hello! How can I help?" {
		t.Errorf("content = %q", cc.Choices[0].Message.Content)
	}
	if cc.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", cc.Choices[0].FinishReason)
	}
	if cc.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens = %d, want 10", cc.Usage.PromptTokens)
	}
	if cc.Usage.CompletionTokens != 8 {
		t.Errorf("completion_tokens = %d, want 8", cc.Usage.CompletionTokens)
	}
}

func TestResponsesToChatCompletion_Error(t *testing.T) {
	respBody := `{
		"id": "resp_err",
		"object": "response",
		"created_at": 1709900000,
		"model": "gpt-5.4",
		"output": [],
		"error": {
			"message": "Rate limit exceeded",
			"type": "rate_limit_error",
			"code": "rate_limit"
		}
	}`

	out, err := responsesToChatCompletion([]byte(respBody))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}

	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp.Error.Message != "Rate limit exceeded" {
		t.Errorf("error message = %q, want 'Rate limit exceeded'", errResp.Error.Message)
	}
}

// ---------------------------------------------------------------------------
// isStreamingRequest
// ---------------------------------------------------------------------------

func TestIsStreamingRequest(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{`{"model":"gpt-5.4","stream":true}`, true},
		{`{"model":"gpt-5.4","stream":false}`, false},
		{`{"model":"gpt-5.4"}`, false},
		{`{"model":"gpt-5.4","stream":"yes"}`, false},
		{`invalid json`, false},
	}
	for _, tt := range tests {
		got := isStreamingRequest([]byte(tt.body))
		if got != tt.want {
			t.Errorf("isStreamingRequest(%s) = %v, want %v", tt.body, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractAccountIDFromJWT
// ---------------------------------------------------------------------------

func TestExtractAccountIDFromJWT(t *testing.T) {
	payload := map[string]interface{}{
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "acct-test-123",
		},
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	fakeJWT := "eyJhbGciOiJSUzI1NiJ9." + payloadB64 + ".fakesig"

	accountID := extractAccountIDFromJWT(fakeJWT)
	if accountID != "acct-test-123" {
		t.Errorf("accountID = %q, want acct-test-123", accountID)
	}
}

func TestExtractAccountIDFromJWT_Invalid(t *testing.T) {
	accountID := extractAccountIDFromJWT("not-a-jwt")
	if accountID != "" {
		t.Errorf("expected empty, got %q", accountID)
	}
	accountID = extractAccountIDFromJWT("")
	if accountID != "" {
		t.Errorf("expected empty for empty string, got %q", accountID)
	}
}
