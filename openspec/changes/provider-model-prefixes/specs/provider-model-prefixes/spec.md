## ADDED Requirements

### Requirement: Provider model IDs carry provider-namespaced prefixes in the models list
The service SHALL prefix every model ID returned from `/v1/models` with the originating provider's namespace: `gc-` for GitHub Copilot models, `oc-` for OpenAI Codex models.

#### Scenario: Copilot models are prefixed gc-
- **WHEN** a client calls `GET /v1/models` with Copilot enabled
- **THEN** every model ID from the Copilot provider appears as `gc-<original-id>` in the response

#### Scenario: Codex models are prefixed oc-
- **WHEN** a client calls `GET /v1/models` with Codex enabled
- **THEN** every model ID from the Codex provider appears as `oc-<original-id>` in the response

### Requirement: Prefixed model IDs are accepted in request bodies and forwarded bare
The service SHALL accept prefixed model IDs (`gc-*`, `oc-*`) in `/v1/chat/completions` and `/v1/embeddings` request bodies, strip the prefix before forwarding to the upstream provider, and route to the provider indicated by the prefix.

#### Scenario: Request with prefixed model ID routes to correct provider
- **WHEN** a client sends `POST /v1/chat/completions` with `"model": "gc-gpt-4o"`
- **THEN** the request is routed to the Copilot provider with the upstream body using `"model": "gpt-4o"`

#### Scenario: Request with prefixed Codex model routes to Codex
- **WHEN** a client sends `POST /v1/chat/completions` with `"model": "oc-gpt-5.3-codex"`
- **THEN** the request is routed to the Codex provider with the upstream body using `"model": "gpt-5.3-codex"`

### Requirement: Bare model IDs remain valid for backward compatibility
The service SHALL continue to accept bare (unprefixed) model IDs in requests. Routing for bare IDs follows existing rules: `model_map` first, then provider catalog, then default provider fallback when model is omitted.

#### Scenario: Bare model ID routes via model_map
- **WHEN** a client sends a request with `"model": "text-embedding-3-large"` and that ID exists in `routing.model_map`
- **THEN** the request routes to the provider specified in `model_map` for that ID

#### Scenario: Bare model ID that is ambiguous returns an error
- **WHEN** a client sends a request with a bare model ID that is exposed by more than one provider and has no `model_map` entry
- **THEN** the service returns an error indicating the model is ambiguous and instructs the client to use a prefixed ID

### Requirement: model_map entries support both prefixed and bare IDs as keys
The router SHALL support `model_map` keys in both prefixed form (`gc-gpt-4o`) and bare form (`gpt-4o`). Prefixed key lookup takes priority over bare key lookup.

#### Scenario: Prefixed model_map key resolves first
- **WHEN** `model_map` contains both `"gc-gpt-4o"` and `"gpt-4o"` as keys
- **THEN** a request for `gc-gpt-4o` uses the `gc-gpt-4o` entry

#### Scenario: Bare model_map key still works
- **WHEN** `model_map` contains `"text-embedding-3-large": {"provider": "codex"}` (bare key, existing operator config)
- **THEN** a request for `text-embedding-3-large` routes to Codex without requiring operator changes

### Requirement: model_map synthesised model list entries use provider-derived OwnedBy
Models added to the `/v1/models` response from `routing.model_map` entries (when not returned by any provider's API) SHALL have their `owned_by` field derived from the target provider rather than hardcoded to `"openai"`.

#### Scenario: model_map entry for Copilot uses github-copilot owned_by
- **WHEN** a `model_map` entry has `"provider": "copilot"` and is not returned by the Copilot API
- **THEN** the synthesised model entry in `/v1/models` has `"owned_by": "github-copilot"`

#### Scenario: model_map entry for Codex uses openai owned_by
- **WHEN** a `model_map` entry has `"provider": "codex"` and is not returned by the Codex API
- **THEN** the synthesised model entry has `"owned_by": "openai"`
