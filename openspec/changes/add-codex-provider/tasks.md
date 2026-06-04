## 1. Codex auth and transport contract

- [x] 1.1 Audit `src/codex_auth.go`, `src/codex_provider.go`, and `src/responses_adapter.go` against the official-mode expectations already captured in repo docs.
- [x] 1.2 Split Codex credential handling so `api_key` mode and ChatGPT-backed/device-auth mode use explicit, provider-safe credential sources.
- [x] 1.3 Split Codex request building and upstream transport behavior by auth mode instead of relying on one generic adapter path.
- [x] 1.4 Ensure logs, status output, and config persistence never expose Codex secret material.

## 2. Multi-provider routing and model exposure

- [x] 2.1 Update routing behavior so chat and embeddings resolution stays provider-aware by model and capability.
- [x] 2.2 Harden ambiguous-model handling and `routing.model_map` behavior for multi-provider operation.
- [x] 2.3 Verify `/v1/models` merges provider catalogs while preserving configured model visibility and provider isolation.
- [x] 2.4 Verify provider-specific status/health behavior for Copilot-only, Codex-only, and mixed configurations.

## 3. CLI, tests, and regression coverage

- [x] 3.1 Align `auth codex`, status, and related CLI behavior with the supported Codex modes and credential sources.
- [x] 3.2 Add or update unit tests for Codex auth-mode selection, routing resolution, ambiguous-model behavior, and request normalization.
- [x] 3.3 Add or update integration/regression tests for mixed-provider model listing, chat routing, embeddings routing, and provider-isolation behavior.
- [x] 3.4 Run `make fmt && make vet && make test && make build` and address any change-related failures before merging.
