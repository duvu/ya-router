## Why

The Rust port (`rust/`) added significant scaffolding complexity — a separate workspace, Cargo dependencies, Makefile targets, and test suites — but is not yet functional as a production runtime and is not deployed. Retaining it adds maintenance burden without current benefit, so it should be removed until a future effort can commit to full parity. Additionally, the `allowed_models` filter in both the Copilot and Codex providers silently restricts which models are exposed; removing the gate allows operators and clients to see all models a provider actually offers, which is the expected behaviour for an open proxy.

## What Changes

- **BREAKING (Rust workspace removed)**: Delete `rust/` directory and all Rust-related Makefile targets (`rust-build`, `rust-test`, `rust-check`, `rust-run`, `rust-fmt`). The Go runtime remains the only runtime.
- Remove Rust-related OpenSpec changes (`port-project-to-rust`, `rust-complete-port`) from active tracking.
- Remove `filterAllowedModels` gate from `CopilotProvider.ListModels` — Copilot now returns all models from upstream without filtering.
- Remove `filterAllowedModels` gate from `CodexProvider.ListModels` — Codex now returns all hardcoded models without filtering.
- Keep `AllowedModels` field in config structs for backward compatibility (no config migration needed; the field simply has no effect on the models list).
- Keep `filterAllowedModels` and `isModelAllowedWithPrefix` functions; they may still be useful for future routing features and are harmless to retain.
- Update `config.example.json` to remove the illustrative `allowed_models` entries (or clarify they are now unused for listing).
- Update `docs/CONFIGURATION.md` to reflect that `allowed_models` no longer gates the model list.

## Capabilities

### New Capabilities
- `open-model-listing`: Providers expose all available models regardless of `allowed_models` config. Both Copilot and Codex `ListModels` return full provider model sets.

### Modified Capabilities
- `provider-model-prefixes`: The prefix-injection in `modelsHandler` still applies, but now operates on the full provider model set rather than a filtered subset.

## Impact

- `src/copilot_provider.go` — remove `filterAllowedModels` call in `ListModels`
- `src/codex_provider.go` — remove `filterAllowedModels` call in `ListModels`
- `rust/` — entire directory deleted
- `Makefile` — rust-* targets removed
- `config.example.json` — `allowed_models` entries clarified or emptied
- `docs/CONFIGURATION.md` — `allowed_models` section updated
- OpenSpec changes `port-project-to-rust` and `rust-complete-port` — mark archived or note as superseded
- Tests in `src/*_test.go` that rely on `allowed_models` filtering behaviour in `ListModels` — update to reflect new pass-through behaviour
