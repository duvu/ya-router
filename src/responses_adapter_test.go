// responses_adapter_test.go — Tests for Chat Completions ↔ Responses API adapter.
package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestChatToResponsesBody_Basic(t *testing.T) {
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

	out, err := chatToResponsesBody([]byte(input))
	if err != nil {
		t.Fatalf("chatToResponsesBody: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// "messages" should be renamed to "input".
	if _, ok := m["messages"]; ok {
		t.Error("output contains 'messages' — should be renamed to 'input'")
	}
	if _, ok := m["input"]; !ok {
		t.Error("output missing 'input'")
	}

	// "max_tokens" should be renamed to "max_output_tokens".
	if _, ok := m["max_tokens"]; ok {
		t.Error("output contains 'max_tokens' — should be renamed to 'max_output_tokens'")
	}
	if _, ok := m["max_output_tokens"]; !ok {
		t.Error("output missing 'max_output_tokens'")
	}

	// "model", "temperature", "stream" should be preserved.
	for _, key := range []string{"model", "temperature", "stream"} {
		if _, ok := m[key]; !ok {
			t.Errorf("output missing preserved key %q", key)
		}
	}
}

func TestChatToResponsesBody_DropsUnsupported(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hi"}],
		"n": 2,
		"stop": ["\n"],
		"frequency_penalty": 0.5,
		"presence_penalty": 0.5
	}`

	out, err := chatToResponsesBody([]byte(input))
	if err != nil {
		t.Fatalf("chatToResponsesBody: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	for _, key := range []string{"n", "stop", "frequency_penalty", "presence_penalty"} {
		if _, ok := m[key]; ok {
			t.Errorf("output should not contain dropped key %q", key)
		}
	}
}

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
			Index        int `json:"index"`
			Message      struct {
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
		t.Errorf("content = %q, want 'Hello! How can I help?'", cc.Choices[0].Message.Content)
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

func TestChatToResponsesBody_MaxCompletionTokens(t *testing.T) {
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}],"max_completion_tokens":200}`

	out, err := chatToResponsesBody([]byte(input))
	if err != nil {
		t.Fatalf("chatToResponsesBody: %v", err)
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

func TestJwtClaims(t *testing.T) {
	// Build a fake JWT: header.payload.signature
	payload := map[string]string{
		"organization_id": "org-abc123",
		"project_id":      "proj-xyz",
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	fakeJWT := "eyJhbGciOiJSUzI1NiJ9." + payloadB64 + ".fakesig"

	org, proj := jwtClaims(fakeJWT)
	if org != "org-abc123" {
		t.Errorf("orgID = %q, want org-abc123", org)
	}
	if proj != "proj-xyz" {
		t.Errorf("projectID = %q, want proj-xyz", proj)
	}
}

func TestJwtClaims_Invalid(t *testing.T) {
	org, proj := jwtClaims("not-a-jwt")
	if org != "" || proj != "" {
		t.Errorf("expected empty, got org=%q proj=%q", org, proj)
	}
	org, proj = jwtClaims("")
	if org != "" || proj != "" {
		t.Errorf("expected empty for empty string, got org=%q proj=%q", org, proj)
	}
}
