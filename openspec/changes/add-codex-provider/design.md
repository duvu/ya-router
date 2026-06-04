## Context

This repository is already structured around a provider abstraction (`src/provider.go`) and a model router (`src/router.go`), and it already contains Codex-specific code in `src/codex_auth.go` and `src/codex_provider.go`. However, repo-local design notes and request cards show that the current Codex implementation still has contract drift: ChatGPT-backed Codex mode and API-key mode do not share the same backend contract, request normalization differs by transport, and the CLI/README contract has not always matched the real code path.

The change therefore is not “add a brand-new provider from scratch”; it is “finish and harden Codex as a first-class provider beside Copilot using verified official Codex behavior.”

## Goals / Non-Goals

**Goals:**
- Make Codex usable beside Copilot in the same running service.
- Split Codex behavior by auth mode so `api_key` and ChatGPT-backed flows use the correct credential source and transport.
- Keep request routing provider-aware for chat, embeddings, and `/v1/models`.
- Preserve the OpenAI-compatible external API while handling backend-specific differences inside the provider/adapter layer.
- Prevent Codex secret material from being logged or exposed through config, status, or API responses.

**Non-Goals:**
- Supporting every OpenAI or Codex endpoint beyond the current service surface.
- Changing the public API away from OpenAI-compatible `/v1/*` endpoints.
- Building a dedicated external auth service for Codex.
- Solving unrelated Copilot behavior outside the shared provider-routing surface.

## Decisions

### 1. Treat Codex as an existing-but-incomplete first-class provider
The repo already has `ProviderCodex`, `CodexProvider`, and provider-aware routing. The design will extend those existing seams instead of introducing a second architecture just for Codex.

**Alternatives considered:**
- Remove the current provider abstraction and re-centralize proxy behavior in `proxy.go`.
- Add Codex as a special case outside `Provider`.

Both alternatives would increase drift and make a future third provider harder.

### 2. Split Codex credential sources by auth mode
`api_key` mode SHALL use OpenAI Platform credentials. ChatGPT-backed/device-auth mode SHALL use the official Codex credential substrate and any required metadata such as account identity.

**Alternatives considered:**
- Treat every device-auth token like a generic Platform API bearer token.
- Persist all ChatGPT-backed credentials into this service’s own config as the canonical source.

The repo’s own request cards show that these approaches are the source of current production breakage and contract drift.

### 3. Split Codex transports by backend contract
Platform mode and ChatGPT-backed mode SHALL have distinct request-building and transport behavior. Backend-specific request sanitization belongs in transport-specific builders, not a single generic pass-through adapter.

**Alternatives considered:**
- Keep one generic request conversion path and patch unsupported fields ad hoc.
- Forward most unknown OpenAI-compatible fields upstream in every mode.

The repo’s documented `stream_options` failure shows those approaches are too weak.

### 4. Keep multi-provider routing centered in the existing router + provider registry
`src/router.go`, `src/models.go`, and provider `ListModels`/`Capabilities` will remain the integration points for request resolution and merged model exposure. Codex changes should plug into those existing seams rather than duplicate routing logic.

**Alternatives considered:**
- Route Codex entirely inside `proxy.go`.
- Hardcode provider selection from endpoint or auth mode alone.

Those alternatives would break the repo’s existing provider-aware direction and make ambiguity handling worse.

### 5. Keep the public API stable while making provider ownership observable
`/v1/models`, chat, and embeddings stay OpenAI-compatible, but internal and operator-visible behavior SHALL remain provider-aware through routing metadata, status output, and logs.

**Alternatives considered:**
- Expose only opaque merged models with no provider traceability.
- Force namespaced client model IDs immediately.

The first hides critical routing state; the second is more disruptive than needed for this change.

## Risks / Trade-offs

- **ChatGPT-backed Codex contract may evolve** → Isolate that behavior inside Codex-specific credential and transport layers so changes do not leak into generic routing.
- **Some model IDs may overlap across providers** → Keep explicit `routing.model_map` as the tie-breaker and return clear ambiguity errors when needed.
- **Secret-handling mistakes could leak credentials** → Treat `~/.codex/auth.json` and any equivalent store as secret material; never expose token contents through logs, status, or tests.
- **Copilot regressions while hardening shared routing paths** → Preserve provider isolation and keep regression coverage for Copilot-only flows.

## Migration Plan

- Keep existing provider abstraction and router wiring intact.
- Move Codex behavior behind explicit auth-mode and transport boundaries.
- Preserve OpenAI-compatible endpoint shape so existing clients do not need to change.
- Verify Copilot-only configs and mixed Copilot+Codex configs both continue to work.

## Open Questions

- Whether ChatGPT-backed model discovery should remain based on curated/known model catalogs or gain a stronger official-source refresh path.
- Whether ambiguous model IDs should stay un-namespaced with explicit config rules or gain a namespaced fallback later.
