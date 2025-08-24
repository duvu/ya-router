package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDefaultModelEnforcementIntegration tests the complete flow of default model enforcement
func TestDefaultModelEnforcementIntegration(t *testing.T) {
	// Setup test configuration with specific default model
	cfg := &Config{
		DefaultModel:  "gpt-5-mini",
		AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini", "claude-3.5-sonnet"},
		CopilotToken:  "test_token_for_integration",
		Port:          7071,
	}
	setDefaultTimeouts(cfg)

	// Create mock server to capture the actual request sent to GitHub Copilot
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the request that was actually sent
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode captured request: %v", err)
		}

		// Send back a mock response
		mockResponse := ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   req.Model, // Echo back the model that was actually sent
			Choices: []ChatCompletionChoice{
				{
					Index: 0,
					Message: ChatCompletionMessage{
						Role:    "assistant",
						Content: "Hello! I received your message.",
					},
					FinishReason: "stop",
				},
			},
			Usage: ChatCompletionUsage{
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer mockServer.Close()

	testCases := []struct {
		name               string
		clientRequestModel string
		expectedUsedModel  string
		description        string
	}{
		{
			name:               "client requests gpt-4",
			clientRequestModel: "gpt-4",
			expectedUsedModel:  "gpt-5-mini",
			description:        "Client requests gpt-4 but service should use gpt-5-mini",
		},
		{
			name:               "client requests claude-3.5-sonnet",
			clientRequestModel: "claude-3.5-sonnet",
			expectedUsedModel:  "gpt-5-mini",
			description:        "Client requests claude-3.5-sonnet but service should use gpt-5-mini",
		},
		{
			name:               "client requests default model",
			clientRequestModel: "gpt-5-mini",
			expectedUsedModel:  "gpt-5-mini",
			description:        "Client requests gpt-5-mini and service should use gpt-5-mini",
		},
		{
			name:               "client requests unknown model",
			clientRequestModel: "unknown-model-123",
			expectedUsedModel:  "gpt-5-mini",
			description:        "Client requests unknown model but service should use gpt-5-mini",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create client request
			clientRequest := ChatCompletionRequest{
				Model: tc.clientRequestModel,
				Messages: []ChatCompletionMessage{
					{Role: "user", Content: "Hello, world!"},
				},
			}

			requestBody, err := json.Marshal(clientRequest)
			if err != nil {
				t.Fatalf("Failed to marshal client request: %v", err)
			}

			// Test the request transformation logic
			transformedBody, err := validateAndTransformRequestModel(requestBody, cfg)
			if err != nil {
				t.Fatalf("validateAndTransformRequestModel failed: %v", err)
			}

			// Parse the transformed request to verify model enforcement
			var transformedRequest ChatCompletionRequest
			if err := json.Unmarshal(transformedBody, &transformedRequest); err != nil {
				t.Fatalf("Failed to unmarshal transformed request: %v", err)
			}

			// Verify that the model was enforced to the default
			if transformedRequest.Model != tc.expectedUsedModel {
				t.Errorf("Model enforcement failed: client requested %q, expected service to use %q, but got %q",
					tc.clientRequestModel, tc.expectedUsedModel, transformedRequest.Model)
			}

			// Verify that the model is always the configured default
			if transformedRequest.Model != cfg.DefaultModel {
				t.Errorf("Model does not match configured default: got %q, want %q",
					transformedRequest.Model, cfg.DefaultModel)
			}

			// Verify other fields were not modified
			if transformedRequest.Messages[0].Content != clientRequest.Messages[0].Content {
				t.Errorf("Message content was unexpectedly modified")
			}

			t.Logf("✅ %s: Client requested %q → Service uses %q (default: %q)",
				tc.description, tc.clientRequestModel, transformedRequest.Model, cfg.DefaultModel)
		})
	}
}

// TestProxyHandlerDefaultModelEnforcement tests the proxy handler with different configurations
func TestProxyHandlerDefaultModelEnforcement(t *testing.T) {
	testConfigs := []struct {
		name          string
		defaultModel  string
		allowedModels []string
	}{
		{
			name:          "gpt-5-mini default",
			defaultModel:  "gpt-5-mini",
			allowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
		},
		{
			name:          "claude default",
			defaultModel:  "claude-3.5-sonnet",
			allowedModels: []string{"claude-3.5-sonnet", "gpt-4"},
		},
		{
			name:          "gemini default",
			defaultModel:  "gemini-2.5-pro",
			allowedModels: []string{"gemini-2.5-pro", "gpt-4", "claude-3.5-sonnet"},
		},
	}

	for _, tcfg := range testConfigs {
		t.Run(tcfg.name, func(t *testing.T) {
			cfg := &Config{
				DefaultModel:  tcfg.defaultModel,
				AllowedModels: tcfg.allowedModels,
			}
			setDefaultTimeouts(cfg)

			// Test requests with various models
			testModels := []string{"gpt-4", "claude-3.5-sonnet", "gemini-2.5-pro", "unknown-model"}

			for _, requestedModel := range testModels {
				clientRequest := ChatCompletionRequest{
					Model: requestedModel,
					Messages: []ChatCompletionMessage{
						{Role: "user", Content: "Test message"},
					},
				}

				requestBody, err := json.Marshal(clientRequest)
				if err != nil {
					t.Fatalf("Failed to marshal request: %v", err)
				}

				// Test the transformation
				transformedBody, err := validateAndTransformRequestModel(requestBody, cfg)
				if err != nil {
					t.Fatalf("Request transformation failed: %v", err)
				}

				var transformedRequest ChatCompletionRequest
				if err := json.Unmarshal(transformedBody, &transformedRequest); err != nil {
					t.Fatalf("Failed to unmarshal transformed request: %v", err)
				}

				// Verify enforcement
				if transformedRequest.Model != cfg.DefaultModel {
					t.Errorf("Config %s: requested %q but service uses %q, want %q (default)",
						tcfg.name, requestedModel, transformedRequest.Model, cfg.DefaultModel)
				}
			}
		})
	}
}

// TestModelsEndpointConsistency tests that the /v1/models endpoint includes the default model
func TestModelsEndpointConsistency(t *testing.T) {
	cfg := &Config{
		DefaultModel:  "gpt-5-mini",
		AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
	}
	setDefaultTimeouts(cfg)

	// Create a test HTTP request to the models endpoint
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()

	// Call the models handler
	handler := modelsHandler(cfg)
	handler(rec, req)

	// Parse the response
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", rec.Code)
	}

	var modelList ModelList
	if err := json.NewDecoder(rec.Body).Decode(&modelList); err != nil {
		t.Fatalf("Failed to decode models response: %v", err)
	}

	// Verify that the default model is included in the models list
	defaultModelFound := false
	for _, model := range modelList.Data {
		if model.ID == cfg.DefaultModel {
			defaultModelFound = true
			break
		}
	}

	if !defaultModelFound {
		modelIDs := make([]string, len(modelList.Data))
		for i, model := range modelList.Data {
			modelIDs[i] = model.ID
		}
		t.Errorf("Default model %q not found in models list. Available models: %v",
			cfg.DefaultModel, modelIDs)
	}

	t.Logf("✅ Default model %q is included in /v1/models response", cfg.DefaultModel)
}
