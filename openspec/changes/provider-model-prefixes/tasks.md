## 1. Prefix Infrastructure

- [x] 1.1 Add prefix constants `CopilotModelPrefix = "gc-"` and `CodexModelPrefix = "oc-"` to `src/models.go` (or new `src/prefixes.go`), plus helpers `AddModelPrefix(providerID, modelID)`, `StripModelPrefix(modelID) (bare, providerID, ok)`, and `ProviderPrefix(providerID) string`
- [x] 1.2 Add unit tests for `AddModelPrefix`, `StripModelPrefix`, and `ProviderPrefix` covering both known providers and unknown/bare input

## 2. Models Handler Updates

- [x] 2.1 Update `modelsHandler` in `src/models.go` to apply provider prefix to each model from `p.ListModels`: call `AddModelPrefix(p.ID(), m.ID)` before appending to `allModels`
- [x] 2.2 Update `modelsHandler` synthesised `model_map` entries to derive `OwnedBy` from the entry's `provider` field (`"github-copilot"` for Copilot, `"openai"` for Codex) instead of hardcoding `"openai"`
- [x] 2.3 Update `filterAllowedModels` to tolerate prefixed IDs: when checking `isModelAllowed`, also check the bare ID against the allowed list and the prefixed ID against the allowed list
- [x] 2.4 Add or update tests for prefixed model list output, provider-derived OwnedBy, and prefixed allowed_models filtering

## 3. Router Prefix Awareness

- [x] 3.1 Update `ModelRouter.Resolve` in `src/router.go` to strip the prefix from the requested model ID as the first step: if the prefix identifies a specific provider, restrict catalog search to that provider only; return an error if the model is unavailable from that provider
- [x] 3.2 Update `model_map` lookup to try the full key (prefixed or bare) first, then the bare ID as fallback, so existing operator `model_map` entries without prefixes continue to work
- [x] 3.3 Update ambiguity error messages to instruct the client to use a prefixed model ID when a bare model is available from more than one provider
- [x] 3.4 Add unit tests for prefix-aware routing: `gc-` routes to Copilot only, `oc-` routes to Codex only, wrong-provider prefix returns error, bare ambiguous model returns informative error, bare unambiguous model still routes, `model_map` entry takes priority over prefix routing

## 4. Proxy / Transport Layer

- [x] 4.1 Confirm that `processProxyRequest` in `src/proxy.go` uses the router-resolved (bare) model ID when patching the request body before forwarding; add a test or log to verify the upstream never receives a prefixed model ID

## 5. Rust Skeleton Sync

- [x] 5.1 Update `visible_models` in `rust/src/models.rs` to apply the same `gc-`/`oc-` prefix constants to models from each provider, keeping the Rust model list consistent with Go
- [x] 5.2 Add or update Rust tests in `rust/src/` to verify prefixed model IDs appear in the visible model list

## 6. Config Example Update

- [x] 6.1 Update `config.example.json` `routing.model_map` example to show a prefixed key (`"oc-text-embedding-3-large"`) alongside the existing bare key example, with a comment explaining the convention

## 7. Operator Configuration Guide

- [x] 7.1 Create `docs/CONFIGURATION.md` with sections: Overview, Quick Start, Configuration File Reference (all fields with types/defaults/descriptions), GitHub Copilot Setup (auth flow, device_code, troubleshooting), OpenAI Codex Setup (api_key mode, chatgpt/device_code mode, official auth store, troubleshooting), Model Prefix Convention (`gc-*` / `oc-*`, backward compatibility, examples), Routing Configuration (model_map, default_provider, default_model, show_unavailable_models, examples), Timeout Reference, Running with Docker, Common Errors and Solutions
- [x] 7.2 Cross-check `docs/CONFIGURATION.md` against `config.example.json` fields to ensure every field is documented; add any missing fields

## 8. Test Coverage Update

- [x] 8.1 Update `src/integration_test.go` existing models endpoint test to expect prefixed model IDs (`gc-*` and `oc-*`) in the merged list
- [x] 8.2 Add integration test for a prefixed chat request (`gc-<model>`) verifying it is routed to Copilot and forwarded with bare model ID
- [x] 8.3 Update `src/proxy_test.go` tests that assert specific model IDs to use prefixed form where appropriate

## 9. Verification

- [x] 9.1 Run `make fmt && make vet && make test && make build`; confirm all pass with zero new failures
- [x] 9.2 Run `cargo test --manifest-path rust/Cargo.toml && cargo check --manifest-path rust/Cargo.toml`; confirm Rust tests pass
