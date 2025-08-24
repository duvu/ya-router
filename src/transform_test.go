package main

import (
	"encoding/json"
	"testing"
)

func TestValidateAndTransformModel(t *testing.T) {
	tests := []struct {
		name           string
		requestedModel string
		config         *Config
		expectedModel  string
		description    string
	}{
		{
			name:           "enforces default model regardless of requested model",
			requestedModel: "gpt-4",
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
			},
			expectedModel: "gpt-5-mini",
			description:   "Should return default model even when requesting allowed model",
		},
		{
			name:           "enforces default model for disallowed requested model",
			requestedModel: "claude-3.5-sonnet",
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
			},
			expectedModel: "gpt-5-mini",
			description:   "Should return default model when requesting disallowed model",
		},
		{
			name:           "enforces default model even when requested model matches default",
			requestedModel: "gpt-5-mini",
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
			},
			expectedModel: "gpt-5-mini",
			description:   "Should return default model when requesting same model as default",
		},
		{
			name:           "enforces default model with empty allowed models list",
			requestedModel: "any-model",
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{},
			},
			expectedModel: "gpt-5-mini",
			description:   "Should return default model even with empty allowed models list",
		},
		{
			name:           "enforces default model with nil allowed models list",
			requestedModel: "any-model",
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: nil,
			},
			expectedModel: "gpt-5-mini",
			description:   "Should return default model even with nil allowed models list",
		},
		{
			name:           "works with different default model",
			requestedModel: "gpt-4",
			config: &Config{
				DefaultModel:  "claude-3.5-sonnet",
				AllowedModels: []string{"gpt-4", "claude-3.5-sonnet"},
			},
			expectedModel: "claude-3.5-sonnet",
			description:   "Should work with non-gpt default models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateAndTransformModel(tt.requestedModel, tt.config)

			if result != tt.expectedModel {
				t.Errorf("validateAndTransformModel() = %v, want %v. %s", result, tt.expectedModel, tt.description)
			}

			// Additional check: result should always equal the configured default
			if result != tt.config.DefaultModel {
				t.Errorf("Result %v does not match configured DefaultModel %v. Default model enforcement failed.", result, tt.config.DefaultModel)
			}
		})
	}
}

func TestDefaultModelEnforcementInRequestValidation(t *testing.T) {
	// Test that validateAndTransformRequestModel properly enforces default model
	testCases := []struct {
		name         string
		requestBody  string
		config       *Config
		expectChange bool
		description  string
	}{
		{
			name:        "transforms request with different model",
			requestBody: `{"model": "gpt-4", "messages": [{"role": "user", "content": "test"}]}`,
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{"gpt-4", "gpt-5-mini"},
			},
			expectChange: true,
			description:  "Should transform request body to use default model",
		},
		{
			name:        "transforms request even when model matches default",
			requestBody: `{"model": "gpt-5-mini", "messages": [{"role": "user", "content": "test"}]}`,
			config: &Config{
				DefaultModel:  "gpt-5-mini",
				AllowedModels: []string{"gpt-4", "gpt-5-mini"},
			},
			expectChange: false, // No change because model already matches default
			description:  "Should handle request that already has default model",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalBody := []byte(tc.requestBody)
			transformedBody, err := validateAndTransformRequestModel(originalBody, tc.config)

			if err != nil {
				t.Fatalf("validateAndTransformRequestModel() error = %v", err)
			}

			// Parse both bodies to check if model was enforced
			var originalReq, transformedReq ChatCompletionRequest

			if err := json.Unmarshal(originalBody, &originalReq); err != nil {
				t.Fatalf("Failed to parse original request: %v", err)
			}

			if err := json.Unmarshal(transformedBody, &transformedReq); err != nil {
				t.Fatalf("Failed to parse transformed request: %v", err)
			}

			// The transformed request should always use the default model
			if transformedReq.Model != tc.config.DefaultModel {
				t.Errorf("Transformed request model = %v, want %v (default model enforcement failed)",
					transformedReq.Model, tc.config.DefaultModel)
			}

			// Check if change was expected
			changed := originalReq.Model != transformedReq.Model
			if changed != tc.expectChange {
				t.Errorf("Expected change = %v, but got change = %v", tc.expectChange, changed)
			}
		})
	}
}

// TestDefaultModelEnforcementConsistency ensures that the enforcement is consistent
// across multiple calls with the same configuration
func TestDefaultModelEnforcementConsistency(t *testing.T) {
	config := &Config{
		DefaultModel:  "gpt-5-mini",
		AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini", "claude-3.5-sonnet"},
	}

	// Test multiple different requested models
	requestedModels := []string{
		"gpt-4",
		"gpt-4.1",
		"claude-3.5-sonnet",
		"o1-preview",
		"gemini-pro",
		"", // empty string
		"unknown-model",
	}

	for _, requestedModel := range requestedModels {
		result := validateAndTransformModel(requestedModel, config)

		if result != config.DefaultModel {
			t.Errorf("Inconsistent enforcement: requested=%q, got=%q, want=%q",
				requestedModel, result, config.DefaultModel)
		}
	}
}
