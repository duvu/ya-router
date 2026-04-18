// transform.go — request body parsing and model-field utilities.
package main

import (
	"encoding/json"
	"log"
	"strings"
)

// extractModelFromBody parses the "model" field from a JSON request body.
// Returns "" if the field is absent or the body cannot be parsed.
func extractModelFromBody(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Model
}

// patchBodyModel returns body with the top-level "model" field set to model.
// On any parse error the original body is returned unchanged.
func patchBodyModel(body []byte, model string) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		log.Printf("patchBodyModel: cannot unmarshal body: %v", err)
		return body
	}
	m["model"] = model
	patched, err := json.Marshal(m)
	if err != nil {
		log.Printf("patchBodyModel: cannot re-marshal body: %v", err)
		return body
	}
	return patched
}

// isModelAllowed reports whether model appears in the allowed list.
// An empty list means "allow everything".
func isModelAllowed(model string, allowedModels []string) bool {
	if len(allowedModels) == 0 {
		return true
	}
	for _, allowed := range allowedModels {
		if strings.EqualFold(model, allowed) {
			return true
		}
	}
	return false
}

// OpenAI-compatible request/response structures shared across the package.

// ChatCompletionRequest is the standard OpenAI chat completions payload.
type ChatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []ChatCompletionMessage `json:"messages"`
	Temperature *float64                `json:"temperature,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
}

// ChatCompletionMessage is a single message in a chat conversation.
type ChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionResponse is the standard OpenAI chat completions response.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

// ChatCompletionChoice is one generated completion.
type ChatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      ChatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

// ChatCompletionUsage holds token usage statistics.
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelList is the standard OpenAI /v1/models response envelope.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model describes a single model entry.
type Model struct {
	ID                  string   `json:"id"`
	Object              string   `json:"object"`
	Created             int64    `json:"created"`
	OwnedBy             string   `json:"owned_by"`
	Name                string   `json:"name,omitempty"`
	Vendor              string   `json:"vendor,omitempty"`
	Version             string   `json:"version,omitempty"`
	ModelPickerEnabled  bool     `json:"model_picker_enabled,omitempty"`
	ModelPickerCategory string   `json:"model_picker_category,omitempty"`
	Preview             bool     `json:"preview,omitempty"`
	SupportedEndpoints  []string `json:"supported_endpoints,omitempty"`
}
