## Context

The service exposes `/v1/models` by merging model lists from all enabled providers. Currently model IDs are bare upstream identifiers (e.g., `gpt-4o`, `claude-3.5-sonnet`, `gpt-5.3-codex`). When both Copilot and Codex are enabled, the same base model name can appear from both providers, and there is no way for a client or `routing.model_map` entry to unambiguously target one provider over the other.

The router (`src/router.go`) resolves model IDs by:
1. Checking `routing.model_map` first (highest priority).
2. Auto-discovering the model in each provider's allowed model catalog.
3. Falling back to the default provider if the model was omitted.

The prefix layer must integrate at the boundary between the external API surface and the internal routing/forwarding pipeline.

## Goals / Non-Goals

**Goals:**

- Return prefixed model IDs (`gc-<id>` for Copilot, `oc-<id>` for Codex) from `/v1/models`.
- Accept prefixed model IDs in request bodies for `/v1/chat/completions` and `/v1/embeddings`; strip the prefix before forwarding to the upstream.
- Make the router prefix-aware: `gc-gpt-4o` resolves deterministically to Copilot, `oc-gpt-5` to Codex.
- Preserve backward-compatible routing for bare model IDs (existing `model_map` entries and `allowed_models` entries still work without prefixes).
- Mirror the prefix convention in `rust/src/models.rs` so the Rust skeleton stays consistent with Go.
- Ship a complete `docs/CONFIGURATION.md` operator guide.

**Non-Goals:**

- Changing the upstream API call — the prefix is a proxy-internal namespace identifier only.
- Adding prefix config knobs (prefixes are hardcoded constants `gc-` / `oc-`; no per-operator override).
- Changing the `routing.model_map` key schema (operators can use prefixed or bare IDs; both work).
- Changing authentication or provider runtime behaviour beyond what is needed for prefix routing.

## Decisions

### 1. Prefix constants are hardcoded, not configurable

**Decision:** `gc-` and `oc-` are package-level constants, not config fields.

**Rationale:** Configurable prefixes add complexity with zero user benefit — the provider identifiers (`copilot`, `codex`) are already fixed in this service. Making prefixes config-driven would mean operators could break routing by changing them, and tooling (e.g., `model_map` examples, the config guide) could not rely on their values. Hardcoding also means zero migration risk if an operator reads a new config version.

**Alternatives considered:** Per-provider `model_prefix` field in config — rejected because it adds a new required field with no scenario where a different prefix would be desired.

### 2. Prefix injection happens in `modelsHandler`, not inside each provider's `ListModels`

**Decision:** Provider `ListModels` continues to return bare upstream model IDs. `modelsHandler` in `src/models.go` applies the prefix when assembling the final list.

**Rationale:** Providers should remain unaware of the proxy's naming scheme. Injecting prefixes inside each provider would mean duplicating the prefix constants and breaking the provider's contract (its job is to talk to the upstream, not to apply proxy-internal naming). Centralising prefix injection in `modelsHandler` keeps providers testable in isolation.

**Consequence:** `src/router.go` must strip the prefix before calling into provider-level model matching, not inside the providers themselves.

### 3. Router strips the prefix before provider catalog lookup

**Decision:** `ModelRouter.Resolve` strips any known prefix from the requested model ID before performing `model_map` lookup and provider catalog matching. The stripped (bare) ID is what gets forwarded upstream.

**Rationale:** The router is already the single authoritative point for model → provider resolution. This is the correct and minimal place to add prefix awareness; adding it anywhere else would scatter the logic.

### 4. `model_map` keys support both prefixed and bare IDs

**Decision:** When looking up a model in `model_map`, the router tries the full key (prefixed or bare, as supplied by the client) first, then falls back to the bare ID. This means existing `model_map` entries like `"text-embedding-3-large": {"provider": "codex"}` continue to work without operator changes.

**Rationale:** Forcing operators to immediately update all `model_map` entries would be a hard migration. Supporting both is a one-line fallback in the lookup function.

### 5. Rust `visible_models` applies the same prefix constants

**Decision:** `rust/src/models.rs` adds the same `gc-`/`oc-` prefix logic so the Rust skeleton's model list matches the Go service output.

**Rationale:** The Rust skeleton is used for local testing and parity validation. Diverging here would make parity tests harder to write.

## Risks / Trade-offs

- **Client breakage on upgrade.** Any client hardcoding bare model IDs (e.g., `gpt-4o` in a chat request) will still work if the model is unambiguous (single provider). If both providers expose the same bare model ID and neither is in `model_map`, the router will now return an ambiguity error rather than silently picking one. This is the correct new behaviour and is documented in the config guide.
  → Mitigation: config guide includes explicit upgrade note; bare ID fallback is preserved for single-provider cases.

- **`model_map` OwnedBy field in synthesised entries.** Entries added from `model_map` to the model list currently use `OwnedBy: "openai"`. After this change, `model_map` entries should carry the target provider's name (`"github-copilot"` or `"openai"`) derived from the `provider` field.
  → Mitigation: fix `modelsHandler` to look up `OwnedBy` from the `model_map` entry's provider field.

- **Test surface grows.** Prefix-aware routing adds new test dimensions: prefixed vs bare, Copilot vs Codex, `model_map` hit vs miss. Existing tests that assert bare model IDs in combined-provider scenarios may need updates.
  → Mitigation: tasks include explicit test update step before marking any routing task complete.

## Migration Plan

1. Add prefix constants in a new `src/prefixes.go` (or top of `src/models.go`).
2. Update `modelsHandler` to apply prefixes when building the response list.
3. Update `ModelRouter.Resolve` to strip prefix before lookup and include prefix-to-provider mapping.
4. Update `filterAllowedModels` to tolerate prefixed IDs in the `allowed_models` slice.
5. Update `modelsHandler` synthesised `model_map` entries to use provider-derived `OwnedBy`.
6. Update `rust/src/models.rs` `visible_models`.
7. Update `config.example.json` with prefixed `model_map` examples.
8. Write `docs/CONFIGURATION.md`.
9. Update integration tests.
10. Run `make fmt && make vet && make test && make build && cargo test`.

Rollback: revert `src/models.go`, `src/router.go`, `rust/src/models.rs`. No schema changes, no migration needed.

## Open Questions

- Should `model_map` entries without a `provider` field be prefixed with the default provider's prefix in the `/v1/models` output? **Proposed:** yes — if `model_map` has no `provider`, use `routing.default_provider` to determine the prefix. This will be decided at implementation time.
