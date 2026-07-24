package routing

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

func TestQuotaPriorityRestoresConfiguredOrderAfterQuotaCooldown(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	originalNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = originalNow })

	cooldowns := NewCooldownRegistry()
	cooldowns.now = func() time.Time { return now }
	preferred := NewPreferredTargetRegistry()
	preferred.Set("thiendu", provider.CapabilityChat, "codex/gpt-5.4-mini")

	codex := &umbrellaProvider{
		id:            provider.Codex,
		authenticated: true,
		hasCatalog:    true,
		catalog:       []string{"gpt-5.3-codex-spark", "gpt-5.4-mini"},
	}
	registry := provider.NewRegistry(codex)
	routing := configschema.Routing{
		VirtualModels: map[string]configschema.VirtualModel{
			"thiendu": {
				Strategy: configschema.VirtualModelStrategyQuotaPriority,
				Targets: []string{
					"codex/gpt-5.3-codex-spark",
					"codex/gpt-5.4-mini",
				},
			},
		},
	}
	router := NewRouterWithRegistries(registry, routing, cooldowns, preferred)

	first, err := router.Resolve(context.Background(), "thiendu", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedModel != "gpt-5.3-codex-spark" {
		t.Fatalf("first target = %q, want Spark", first.ResolvedModel)
	}

	// A quota-style 429 without a short Retry-After enters the fixed 24-hour
	// target cooldown.
	router.RecordOutcome(first.Selection, http.StatusTooManyRequests, nil, 0)

	second, err := router.Resolve(context.Background(), "thiendu", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if second.ResolvedModel != "gpt-5.4-mini" {
		t.Fatalf("target during Spark cooldown = %q, want gpt-5.4-mini", second.ResolvedModel)
	}

	now = now.Add(quotaCooldown + time.Second)
	third, err := router.Resolve(context.Background(), "thiendu", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if third.ResolvedModel != "gpt-5.3-codex-spark" {
		t.Fatalf("target after cooldown expiry = %q, want Spark", third.ResolvedModel)
	}
}

func TestQuotaCooldownPersistsAcrossRegistryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cooldowns.json")
	registry, err := OpenCooldownRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	until, entered := registry.Record(
		provider.Codex,
		"gpt-5.3-codex-spark",
		provider.CapabilityChat,
		availability.CooldownQuotaExhausted,
		0,
	)
	if !entered {
		t.Fatal("quota cooldown was not recorded")
	}
	if remaining := time.Until(until); remaining < 23*time.Hour+59*time.Minute {
		t.Fatalf("quota cooldown too short: %s", remaining)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cooldown state permissions = %o, want 600", got)
	}

	reopened, err := OpenCooldownRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := reopened.Cooldowns(provider.Codex)
	if len(active) != 1 {
		t.Fatalf("reopened cooldowns = %+v, want one", active)
	}
	if active[0].Model != "gpt-5.3-codex-spark" || active[0].Reason != availability.CooldownQuotaExhausted {
		t.Fatalf("reopened cooldown = %+v", active[0])
	}
}

func TestShortRetryAfterRemainsBurstCooldown(t *testing.T) {
	reason, hint, ok := cooldownPolicy(http.StatusTooManyRequests, nil, 30*time.Second)
	if !ok {
		t.Fatal("429 was not classified")
	}
	if reason != availability.CooldownRateLimited {
		t.Fatalf("reason = %q, want rate_limited", reason)
	}
	if hint != 30*time.Second {
		t.Fatalf("hint = %s, want 30s", hint)
	}
}

func TestCorruptCooldownStateFailsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cooldowns.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := OpenCooldownRegistry(path)
	if err != nil {
		t.Fatalf("corrupt advisory state prevented startup: %v", err)
	}
	active, expired := registry.Cooldowns(provider.Codex)
	if len(active) != 0 || len(expired) != 0 {
		t.Fatalf("corrupt state produced entries: active=%+v expired=%+v", active, expired)
	}
}
