package main

import (
	"strings"
)

// validateAndTransformModel enforces the configured default model for all requests
// This ensures consistent model usage regardless of client-supplied model identifiers
func validateAndTransformModel(requestedModel string, cfg *Config) string {
	// Always return the configured default model to enforce consistent behavior
	// This prevents model selection bypass and ensures predictable billing/features
	return cfg.DefaultModel
}

// isModelAllowed checks if a model is in the allowed list
func isModelAllowed(model string, cfg *Config) bool {
	// If no allowed models configured, allow everything
	if len(cfg.AllowedModels) == 0 {
		return true
	}

	for _, allowedModel := range cfg.AllowedModels {
		if strings.EqualFold(model, allowedModel) {
			return true
		}
	}
	return false
}

// OpenAI-compatible request/response structures
type ChatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []ChatCompletionMessage `json:"messages"`
	Temperature *float64                `json:"temperature,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
}

type ChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

type ChatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      ChatCompletionMessage `json:"message"`
	FinishReason string                `json:"finish_reason"`
}

type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
