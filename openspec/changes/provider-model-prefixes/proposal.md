## Why

When both Copilot and Codex providers are enabled, their model IDs overlap (e.g., both can expose `gpt-5` or `gpt-4o`), making it impossible for clients to target a specific provider explicitly or for operators to understand which model comes from which backend. Provider-namespaced prefixes (`gc-` for GitHub Copilot, `oc-` for OpenAI Codex) eliminate ambiguity and enable deterministic provider selection by model name. The change also ships a comprehensive operator configuration guide—currently absent—so operators can onboard, configure, and troubleshoot both providers without reading source code.

## What Changes

- **BREAKING** Model IDs returned by `/v1/models` will carry provider prefixes: Copilot models prefixed `gc-`, Codex models prefixed `oc-`. Clients using raw upstream model names will need to update `routing.model_map` or use the prefixed IDs.
- `/v1/chat/completions` and `/v1/embeddings` accept prefixed model IDs in the request body; the proxy strips the prefix before forwarding to the upstream provider.
- `routing.model_map` keys and `allowed_models` entries in config support both prefixed and unprefixed IDs (prefixed takes priority; legacy bare IDs still route correctly).
- The router's `Resolve` function is prefix-aware: a request for `gc-gpt-4o` routes to Copilot; `oc-gpt-5` routes to Codex.
- The Rust `models.rs` `visible_models` function is updated in lockstep with Go so the Rust skeleton stays accurate.
- A new operator configuration guide `docs/CONFIGURATION.md` is published covering: install/auth, config schema reference, Copilot-specific behaviour, Codex auth modes (API key vs ChatGPT/device-auth), model prefix convention, `model_map` usage, routing rules, timeout tuning, and troubleshooting.

## Capabilities

### New Capabilities

- `provider-model-prefixes`: Prefix-based model namespace scheme (`gc-*` Copilot, `oc-*` Codex) applied at the `/v1/models` boundary and stripped at the upstream proxy boundary, with router awareness and backward-compatible bare-ID fallback.
- `operator-configuration-guide`: Comprehensive `docs/CONFIGURATION.md` covering both providers end-to-end.

### Modified Capabilities

- `multi-provider-routing`: Router must resolve prefixed model IDs to the correct provider before performing capability lookup. Existing requirement that explicit model misses return an error remains unchanged; prefix stripping is added to the resolution path.

## Impact

- `src/models.go` — prefix injection in `modelsHandler`
- `src/copilot_provider.go` — prefix stripping when forwarding, optional prefix in `ListModels` output
- `src/codex_provider.go` — same for Codex
- `src/router.go` — prefix-aware `Resolve`
- `src/config.go` — `ModelMapEntry` and `AllowedModels` handling must tolerate prefixed IDs (no schema change; logic change only)
- `src/models.go`, `src/router.go` — deduplification and model_map fallback logic must handle both prefixed and bare IDs
- `rust/src/models.rs` — `visible_models` applies same prefix convention for Rust skeleton consistency
- `config.example.json` — update `model_map` example to show prefixed IDs
- New file `docs/CONFIGURATION.md`
- Existing tests in `src/integration_test.go`, `src/proxy_test.go` must be updated/extended for prefix-aware behaviour
