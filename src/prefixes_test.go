package main

import "testing"

func TestProviderPrefix(t *testing.T) {
	tests := []struct {
		providerID ProviderID
		want       string
	}{
		{ProviderCopilot, "gc-"},
		{ProviderCodex, "oc-"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := ProviderPrefix(tt.providerID)
		if got != tt.want {
			t.Errorf("ProviderPrefix(%q) = %q, want %q", tt.providerID, got, tt.want)
		}
	}
}

func TestProviderOwnedBy(t *testing.T) {
	tests := []struct {
		providerID ProviderID
		want       string
	}{
		{ProviderCopilot, "github-copilot"},
		{ProviderCodex, "openai"},
		{"unknown", "openai"},
	}
	for _, tt := range tests {
		got := ProviderOwnedBy(tt.providerID)
		if got != tt.want {
			t.Errorf("ProviderOwnedBy(%q) = %q, want %q", tt.providerID, got, tt.want)
		}
	}
}

func TestAddModelPrefix(t *testing.T) {
	tests := []struct {
		providerID ProviderID
		modelID    string
		want       string
	}{
		{ProviderCopilot, "gpt-4o", "gc-gpt-4o"},
		{ProviderCodex, "gpt-5.3-codex", "oc-gpt-5.3-codex"},
		{ProviderCopilot, "gc-gpt-4o", "gc-gpt-4o"}, // already prefixed
		{ProviderCodex, "oc-gpt-5", "oc-gpt-5"},     // already prefixed
		{"unknown", "gpt-4o", "gpt-4o"},             // unknown provider: no prefix
		{"", "gpt-4o", "gpt-4o"},                    // empty provider: no prefix
	}
	for _, tt := range tests {
		got := AddModelPrefix(tt.providerID, tt.modelID)
		if got != tt.want {
			t.Errorf("AddModelPrefix(%q, %q) = %q, want %q", tt.providerID, tt.modelID, got, tt.want)
		}
	}
}

func TestStripModelPrefix(t *testing.T) {
	tests := []struct {
		modelID      string
		wantBare     string
		wantProvider ProviderID
		wantOK       bool
	}{
		{"gc-gpt-4o", "gpt-4o", ProviderCopilot, true},
		{"oc-gpt-5.3-codex", "gpt-5.3-codex", ProviderCodex, true},
		{"gc-", "", ProviderCopilot, true}, // bare prefix only
		{"oc-", "", ProviderCodex, true},
		{"gpt-4o", "gpt-4o", "", false}, // no prefix
		{"claude-3.5-sonnet", "claude-3.5-sonnet", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		bare, prov, ok := StripModelPrefix(tt.modelID)
		if bare != tt.wantBare || prov != tt.wantProvider || ok != tt.wantOK {
			t.Errorf("StripModelPrefix(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.modelID, bare, prov, ok, tt.wantBare, tt.wantProvider, tt.wantOK)
		}
	}
}
