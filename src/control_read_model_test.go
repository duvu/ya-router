package yarouter

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	secretpkg "github.com/duvu/ya-router/internal/secret"
	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

func TestControlReadModelListsEveryCompiledProvider(t *testing.T) {
	config := defaultConfig()
	runtimeManager, err := runtimepkg.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := newProviderManager(config, runtimeManager)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = providerManager.Reconcile(context.Background(), nil)
		_ = runtimeManager.Close(context.Background())
	}()

	resources, err := newControlReadModel(runtimeManager, providerManager, nil).Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 || resources[0].Descriptor.ID != providerpkg.Copilot || resources[1].Descriptor.ID != providerpkg.Codex || resources[2].Descriptor.ID != providerpkg.Kilo {
		t.Fatalf("compiled providers missing from read model: %+v", resources)
	}
	if resources[1].Enabled || resources[2].Enabled {
		t.Fatalf("disabled providers were reported enabled: %+v", resources)
	}
}

// TestControlReadModelState_AggregatesProviderRoutingAndTelemetry proves
// State() combines provider health, umbrella routing readiness, and
// telemetry counters into one compact payload without adding a new health
// or cooldown state machine (issue #75).
func TestControlReadModelState_AggregatesProviderRoutingAndTelemetry(t *testing.T) {
	oldRecorder := currentTelemetryRecorder()
	recorder := telemetrypkg.NewRecorder()
	setTelemetryRecorder(recorder)
	t.Cleanup(func() { setTelemetryRecorder(oldRecorder) })
	recorder.Begin("copilot", "gpt-5-mini").Finish(telemetrypkg.Outcome{Success: true, ProducedMessage: true, Usage: &telemetrypkg.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}})

	config := defaultConfig()
	config.Providers.Copilot.Auth.GitHubToken = "gh-token"
	config.Providers.Copilot.Auth.CopilotToken = "cp-token"
	runtimeManager, err := runtimepkg.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := newProviderManager(config, runtimeManager)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = providerManager.Reconcile(context.Background(), nil)
		_ = runtimeManager.Close(context.Background())
	}()

	model := newControlReadModel(runtimeManager, providerManager, nil)
	payload, err := model.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	foundProvider := false
	for _, p := range payload.Providers {
		if p.Provider == "copilot" {
			foundProvider = true
		}
	}
	if !foundProvider {
		t.Fatalf("copilot missing from provider state: %+v", payload.Providers)
	}

	foundThiendu := false
	for _, r := range payload.Routing {
		if r.VirtualModel == "thiendu" {
			foundThiendu = true
		}
	}
	if !foundThiendu {
		t.Fatalf("thiendu missing from routing state: %+v", payload.Routing)
	}

	if len(payload.Counters) != 1 {
		t.Fatalf("counters = %d, want 1", len(payload.Counters))
	}
	counter := payload.Counters[0]
	if counter.Provider != "copilot" || counter.Model != "gpt-5-mini" || counter.Requests != 1 || counter.Successes != 1 {
		t.Fatalf("counter = %+v", counter)
	}
	if counter.Usage.TotalTokens != 3 || counter.Usage.Unavailable {
		t.Fatalf("usage = %+v", counter.Usage)
	}
}

// TestControlReadModelState_RedactsSensitiveContent proves the state
// payload never carries prompts, completions, credentials, or raw upstream
// error strings — only bounded identifiers and enum values.
func TestControlReadModelState_RedactsSensitiveContent(t *testing.T) {
	oldRecorder := currentTelemetryRecorder()
	recorder := telemetrypkg.NewRecorder()
	setTelemetryRecorder(recorder)
	t.Cleanup(func() { setTelemetryRecorder(oldRecorder) })
	recorder.Begin("copilot", "gpt-5-mini").Finish(telemetrypkg.Outcome{Success: false, ErrorCategory: telemetrypkg.ErrorCategoryTransport})

	config := defaultConfig()
	config.Providers.Copilot.Auth.GitHubToken = "super-secret-github-token"
	runtimeManager, err := runtimepkg.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := newProviderManager(config, runtimeManager)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = providerManager.Reconcile(context.Background(), nil)
		_ = runtimeManager.Close(context.Background())
	}()

	model := newControlReadModel(runtimeManager, providerManager, nil)
	payload, err := model.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "super-secret-github-token") {
		t.Fatalf("state payload leaked credential material: %s", encoded)
	}
}

func TestControlConfigurationReadIsRedacted(t *testing.T) {
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })

	release, err := acquireManagedConfigState("read-model-test")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Providers.Copilot.Auth.GitHubToken = "github-secret"
	config.Providers.Copilot.Auth.CopilotToken = "copilot-secret"
	config.Providers.Codex.Accounts = []CodexAccount{{
		ID:    "acct_safe",
		Label: "primary",
		Auth: CodexAuthState{
			AccessToken:  "access-secret",
			RefreshToken: "refresh-secret",
			AccountID:    "upstream-account-id",
		},
	}}
	config.Providers.Kilo.APIKey = "kilo-secret"
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}

	resource, err := (&controlReadModel{}).Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(resource)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"github-secret", "copilot-secret", "access-secret", "refresh-secret", "kilo-secret", "upstream-account-id"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("configuration read leaked %q: %s", forbidden, text)
		}
	}
	if resource.Revision < 2 || resource.Desired == nil || resource.Effective == nil {
		t.Fatalf("configuration metadata incomplete: %+v", resource)
	}
}

func TestControlCatalogRetainsLastKnownGoodAfterRefreshFailure(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	store := &controlCatalogStore{records: make(map[providerpkg.ID]controlCatalogRecord), now: func() time.Time { return now }}
	store.store(providerpkg.Codex, []controlpkg.ModelResource{{
		ID:         "codex/gpt-test",
		UpstreamID: "gpt-test",
		ProviderID: providerpkg.Codex,
		Available:  true,
	}})
	now = now.Add(time.Minute)
	store.markError(providerpkg.Codex)
	resource := store.resource(providerpkg.ProviderStatus{
		Descriptor: providerpkg.Descriptor{ID: providerpkg.Codex},
		Enabled:    true,
		Health:     providerpkg.HealthRecord{ProviderID: providerpkg.Codex, State: providerpkg.StateReady},
	})
	if len(resource.Models) != 1 || resource.Models[0].ID != "codex/gpt-test" {
		t.Fatalf("last-known-good catalog was discarded: %+v", resource)
	}
	if !resource.Stale || resource.LastRefreshError != "catalog_refresh_failed" || resource.AgeSeconds != 60 {
		t.Fatalf("catalog failure metadata missing: %+v", resource)
	}
}

func TestCredentialMetadataUsesManagedCredentialPosture(t *testing.T) {
	config := defaultConfig()
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "copilot/token", "copilot-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "copilot/github_token", "github-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "codex/access_token", "access-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "codex/refresh_token", "refresh-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "kilo/api_key", "kilo-key"); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name        string
		providerID  providerpkg.ID
		refreshable bool
	}{
		{name: "copilot", providerID: providerpkg.Copilot, refreshable: true},
		{name: "codex", providerID: providerpkg.Codex, refreshable: true},
		{name: "kilo", providerID: providerpkg.Kilo},
	} {
		t.Run(test.name, func(t *testing.T) {
			credential := credentialMetadata(config, test.providerID, "default", "default", store)
			if !credential.Configured || credential.Source != string(secretpkg.SourceManaged) || credential.Refreshable != test.refreshable {
				t.Fatalf("credential posture = %+v", credential)
			}
		})
	}
}
