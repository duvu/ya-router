package config

import "testing"

func TestValidateVirtualModelsAcceptsQuotaPriority(t *testing.T) {
	routing := Routing{
		VirtualModels: map[string]VirtualModel{
			"thiendu": {
				Strategy: VirtualModelStrategyQuotaPriority,
				Targets: []string{
					"codex/gpt-5.3-codex-spark",
					"codex/gpt-5.4-mini",
					"github/gpt-5.4-mini",
					"kilo/kilo-auto/free",
				},
			},
		},
	}
	if err := routing.ValidateVirtualModels([]string{"github/", "codex/", "kilo/"}); err != nil {
		t.Fatalf("quota_priority rejected: %v", err)
	}
}
