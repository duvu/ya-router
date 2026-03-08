# PRD: Integrate OpenAI Codex as a Parallel Provider Alongside GitHub Copilot

## Project Context - overview and request
- Goal: extend `github-copilot-svcs` from a single-backend proxy into a multi-provider proxy where `github-copilot` and `openai-codex` run in parallel within the same service.
- Core product requirements:
  - Users can sign in to both GitHub Copilot and OpenAI Codex at the same time.
  - The service routes each request to the correct backend based on the `model` selected by the user.
  - `GET /v1/models` must return a merged model list from both providers, with enough metadata for clients to know which provider owns each model.
  - The new architecture must support adding a third provider later without rewriting all of `proxy.go`.
- Additional technical requirements:
  - Support Codex authentication in at least two modes: `api_key` and `chatgpt/device-auth`.
  - Support headless Codex login through a device-code flow.
  - Do not treat `~/.codex/auth.json` as ordinary config; it must be treated as secret material.

## Scope
In-scope:
- Refactor config, auth, model discovery, routing, proxy flow, and CLI so the service becomes provider-agnostic.
- Build a provider registry for at least two providers: `copilot` and `codex`.
- Define model-to-provider-to-upstream routing behavior.
- Define migration from the current config format to the new one.
- Define security, logging, testing, and rollout requirements.

Out-of-scope:
- Changing the public API away from an OpenAI-compatible shape.
- Supporting every OpenAI/Codex endpoint in phase 1.
- Extracting, copying, or exposing tokens from `~/.codex/auth.json`.
- Building a dedicated auth server for Codex.

## Current state and gaps to close
- The service is currently a single-provider Copilot proxy:
  - Config stores Copilot token data in top-level fields `github_token`, `copilot_token`, `expires_at`, and `refresh_in`: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:20-47).
  - `run` mounts `/v1/models`, `/v1/embeddings`, and `/v1/chat/completions` onto the same `proxyHandler(cfg)`: [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:129-200).
  - `processProxyRequest` hardcodes a single upstream `https://api.githubcopilot.com` and Copilot-specific headers: [`github-copilot-svcs/src/proxy.go`](github-copilot-svcs/src/proxy.go:499-538).
  - `modelsHandler` only fetches models from Copilot and uses one global cache: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:19-53), [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:111-178).
  - Authentication only supports GitHub device flow followed by Copilot token refresh: [`github-copilot-svcs/src/auth.go`](github-copilot-svcs/src/auth.go:50-212).
- Current model behavior conflicts with the new requirement:
  - `validateAndTransformModel()` always returns `cfg.DefaultModel`, ignoring the client-selected model: [`github-copilot-svcs/src/transform.go`](github-copilot-svcs/src/transform.go:7-13).
  - `validateAndTransformRequestModel()` in `proxy.go` depends on that behavior, so it cannot route by the model chosen by the user: [`github-copilot-svcs/src/proxy.go`](github-copilot-svcs/src/proxy.go:53-80).
- The current config only has global `allowed_models` and `default_model`, and has no provider-scoped routing concept: [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:1-20).

## Design principles
1. Provider is the primary abstraction. Auth, model discovery, routing, header policy, and retry policy must live behind provider interfaces.
2. The model chosen by the client is an input to routing. `default_model` is only used when the client does not send a model.
3. Copilot and Codex auth state must be isolated. Failure in provider A must not break provider B.
4. Do not hardcode Codex local auth-cache schema into core business logic; if local cache is read at all, it must sit behind an abstraction.
5. The public API must remain OpenAI-compatible so existing clients continue to work.

## Target architecture

### 1. Required provider abstraction
- Add `ProviderID` with at least: `copilot`, `codex`.
- Define a minimum interface:
  - `Name() string`
  - `Capabilities() []Capability`
  - `EnsureAuthenticated(ctx) error`
  - `ListModels(ctx) (*ModelList, error)`
  - `ProxyChat(ctx, request) (*UpstreamResponse, error)`
  - `ProxyEmbeddings(ctx, request) (*UpstreamResponse, error)`
  - `Health(ctx) ProviderHealth`
- `proxy.go` must not know provider-specific header or token details; those must live inside each provider implementation.

### 2. Required model router
- Add `ModelRouter` or `RoutingResolver` as the single place that decides:
  - effective request model
  - destination provider
  - upstream model id
  - whether the capability is supported
- Resolution flow:
  1. Parse request body.
  2. Read `requested_model`.
  3. If the request does not include a model, use `routing.default_model`.
  4. Resolve against `routing.model_map` or the runtime model catalog.
  5. If the model exists in only one provider, route to that provider.
  6. If the model exists in more than one provider, require an explicit rule; otherwise return `400 ambiguous model`.
  7. If the chosen provider does not support the requested capability, return `400 unsupported model`.

### 3. Unified model catalog
- Each provider manages its own model discovery, cache, and refresh behavior.
- `GET /v1/models` must merge models from all enabled providers.
- Each model entry needs internal metadata:
  - `provider`
  - `upstream_model`
  - `capabilities`
  - `auth_ready`
  - `source`
- The JSON response may keep the OpenAI-compatible core shape, but must expose enough metadata for internal clients to see provider ownership.
- Do not use one global cache for all providers; each provider must have its own cache and TTL.

## Auth and credential-management requirements

### 1. GitHub Copilot
- Keep the current auth flow, but move it into a `copilot` provider implementation.
- Copilot token state must move under `providers.copilot.auth`.
- The existing proactive refresh logic must be encapsulated inside a Copilot auth manager instead of remaining part of the generic proxy flow.

### 2. OpenAI Codex
- Support at least two auth modes:
  - `api_key`
  - `chatgpt_device_auth`
- For `api_key`:
  - Prefer environment variables such as `OPENAI_API_KEY` before reading from config.
  - If the key is stored in config, the config file must still be written as `0600`.
- For `chatgpt_device_auth`:
  - The system must support headless login through a device-code flow.
  - CLI must expose an explicit command for this flow, for example `auth codex --device-auth`.
  - The service must honor `CODEX_HOME`; if not set, default to `~/.codex`.
  - `~/.codex/auth.json` must be treated as secret material and must never be logged, echoed in CLI output, or exposed via an endpoint.
- Implementation stance:
  - Recommended for production or server-side usage: `api_key`.
  - `chatgpt_device_auth` should only be supported for trusted self-hosted or local flows.
- If some Codex auth paths use OS credential stores, implementation must not assume that all credentials always live in a plaintext file.

### 3. Simultaneous login
- `status` must display auth state per provider:
  - `copilot: authenticated/not authenticated`
  - `codex: authenticated/not authenticated`
- Server startup must not fail only because one provider is not signed in, as long as the other provider is still usable.
- `GET /v1/models` should return models only from `auth_ready=true` providers unless the user explicitly enables `show_unavailable_models`.

## External assumptions and constraints for Codex
- The following points come from the research summary provided by the requester, and should be treated as external design constraints:
  - Codex may cache credentials locally under `CODEX_HOME` or default `~/.codex`, commonly in `auth.json`.
  - `auth.json` may contain access, refresh, and id tokens, and may be refreshed in place.
  - Some Codex or MCP auth paths use OS credential stores instead of only plaintext files.
  - Headless login is supported through device-code flow.
  - OpenAI recommends API keys for programmatic workflows.
- Required design consequences:
  - Do not bind the exact schema of `auth.json` into core business logic.
  - If local auth cache is read, it must sit behind a `CodexCredentialSource`.
  - All code, logs, and tests must treat `~/.codex/auth.json` as a secret.

## Proposed config refactor

### 1. New config shape
```json
{
  "port": 7071,
  "routing": {
    "default_model": "gpt-5-mini",
    "default_provider": "codex",
    "show_unavailable_models": false,
    "model_map": {
      "gpt-5-mini": {
        "provider": "codex",
        "upstream_model": "gpt-5-mini"
      },
      "claude-sonnet-4": {
        "provider": "copilot",
        "upstream_model": "claude-sonnet-4"
      }
    }
  },
  "providers": {
    "copilot": {
      "enabled": true,
      "auth": {
        "mode": "device_code"
      },
      "allowed_models": []
    },
    "codex": {
      "enabled": true,
      "auth": {
        "mode": "chatgpt_device_auth",
        "codex_home": "~/.codex",
        "api_key_env": "OPENAI_API_KEY"
      },
      "allowed_models": []
    }
  },
  "timeouts": {
    "http_client": 300,
    "server_read": 30,
    "server_write": 300,
    "server_idle": 120,
    "proxy_context": 300,
    "circuit_breaker": 30,
    "keep_alive": 30,
    "tls_handshake": 10,
    "dial_timeout": 10,
    "idle_conn_timeout": 90
  }
}
```

### 2. Migration from the current config
- Map `github_token`, `copilot_token`, `expires_at`, and `refresh_in` into `providers.copilot.auth`.
- Map top-level `allowed_models` into `providers.copilot.allowed_models` in migration V1 to preserve compatibility.
- Map top-level `default_model` into `routing.default_model`.
- If `default_provider` is missing, default it to `copilot` so migration does not unexpectedly change behavior.
- Add a `config version` field so future migrations do not need to guess the schema.

## Required CLI changes
- CLI must move from generic commands to provider-aware commands:
  - `auth copilot`
  - `auth codex --device-auth`
  - `auth codex --api-key`
  - `status`
  - `status --provider copilot`
  - `status --provider codex`
  - `models`
  - `models --provider all|copilot|codex`
  - `refresh --provider copilot|codex`
- `handleRunWithMigration()` must stop calling `ensureValidToken(cfg)` as one global action: [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:138-149).
- Startup must initialize the provider registry and per-provider auth state before mounting handlers.

## Required request-pipeline changes

### 1. Chat completions
- Remove the behavior that forces every request onto `cfg.DefaultModel`.
- Replace `validateAndTransformModel()` with `resolveRequestRoute()`:
  - do not rewrite the model if the client sent a valid model
  - only apply a default when the client omits the model
  - allow model alias to upstream-model mapping
- After resolution, forward the request through the chosen provider adapter instead of hardcoding Copilot.

### 2. Embeddings
- Embeddings may be supported by different providers.
- Router must check `embeddings` capability by model and provider.
- The current Copilot-specific normalization (`input` string -> array) must become a provider-specific hook and must not be applied universally.

### 3. Streaming
- Streaming must keep working for any provider that supports SSE or chunked responses.
- Retry and circuit-breaker state must not remain global across both Copilot and Codex; each provider must own its own state so one failing provider does not degrade the other.

## Requirements for `/v1/models`
- Response must merge models from all providers that meet the conditions:
  - provider is `enabled`
  - auth is ready, or unavailable models are explicitly allowed to be shown
  - model capability is allowed to be exposed
- Every model must remain traceable back to its source provider.
- If two providers expose the same `id`, one of these mechanisms is required:
  - explicit rule in `routing.model_map`
  - namespaced ids such as `copilot/<model>` and `codex/<model>`
  - hide ambiguous models from the response and log a warning
- Recommended V1 behavior:
  - keep the original `id` when it is not ambiguous
  - add a `provider` field
  - require `routing.model_map` when ambiguity exists

## Observability and security requirements
- Logs must include `provider`, `requested_model`, `resolved_model`, and `route_source`.
- Do not log tokens, auth-file content, refresh tokens, or API keys.
- Health endpoint may be extended with:
  - `providers.copilot.status`
  - `providers.codex.status`
  - `providers.<id>.last_refresh_at`
  - `providers.<id>.last_error`
- Tests and runtime must ensure private permissions on config files and secret files.
- Operations documentation must explicitly state:
  - `~/.codex/auth.json` is a secret
  - `chatgpt_device_auth` should only be used in trusted environments
  - production or public services should prefer `api_key`

## Refactor plan by file
- [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:20-217)
  - split `Config` into `RoutingConfig`, `ProvidersConfig`, and `ProviderAuthConfig`
  - add config versioning and explicit migration
- [`github-copilot-svcs/src/auth.go`](github-copilot-svcs/src/auth.go:14-212)
  - turn into `copilot_auth.go`
  - add `codex_auth.go`
  - add `auth_manager.go`
- [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:12-178)
  - split into provider-specific model caches
  - remove one global shared cache
- [`github-copilot-svcs/src/proxy.go`](github-copilot-svcs/src/proxy.go:53-654)
  - remove hardcoded Copilot URL and headers
  - move request processing to `route -> delegate provider`
  - split retry and circuit-breaker behavior into provider-scoped clients
- [`github-copilot-svcs/src/transform.go`](github-copilot-svcs/src/transform.go:7-27)
  - remove the "always default model" logic
  - replace it with a resolver and alias mapper
- [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:12-260)
  - change command parsing to provider-aware commands

## Acceptance criteria
1. If the client sends `model=A` and `A` belongs to Copilot, the request is routed to Copilot and is not rewritten to `default_model`.
2. If the client sends `model=B` and `B` belongs to Codex, the request is routed to Codex without requiring a separate service.
3. If the client omits `model`, the server uses `routing.default_model` and `routing.default_provider`.
4. Users can sign in to Copilot and Codex independently; loss of auth in one provider does not make the other provider unusable.
5. `GET /v1/models` returns a merged list from both providers and indicates the source provider.
6. `status` shows separate auth state per provider.
7. Legacy config migrates without losing Copilot tokens and without causing unintended behavior changes.
8. The headless flow for Codex is documented and exposed through an explicit CLI command.
9. No test, log, or endpoint exposes `~/.codex/auth.json`, refresh tokens, or API keys.
10. Retry, timeout, circuit breaker, and model cache are provider-scoped.

## Testing requirements
- Unit tests:
  - model resolution
  - ambiguous-model detection
  - legacy-config migration
  - auth-state isolation
  - provider-specific request transformation
- Integration tests:
  - login and status for each provider
  - merged `/v1/models`
  - `/v1/chat/completions` routes to the correct provider by model
  - `/v1/embeddings` routes by supported capability
  - graceful fallback when one provider is unavailable
- Regression tests:
  - Copilot-only config still works after migration
  - streaming still flushes correctly

## Suggested rollout phases
- Phase 1:
  - add provider abstraction
  - migrate Copilot into a provider implementation
  - keep current behavior stable
- Phase 2:
  - add Codex auth, config, models, and status support
  - expose merged `/v1/models`
- Phase 3:
  - enable model-based routing for chat completions
  - split retry, circuit breaker, and cache by provider
- Phase 4:
  - complete embeddings routing
  - harden security, docs, and operational playbooks

## Risks and decisions to settle before coding
- Decide the official transport for Codex provider V1:
  - direct OpenAI/Codex backend
  - or adapter through Codex CLI
- Decide policy when two providers expose the same model id.
- Decide whether the service may read `~/.codex/auth.json` directly or should only authenticate through an official CLI or auth layer.
- Decide the production stance:
  - local or self-hosted deployments may allow `chatgpt_device_auth`
  - public or automated deployments should require `api_key`

## Technical conclusion
- This refactor is not just "add another backend to `proxy.go`"; it is a shift from single-provider proxying to routed multi-provider architecture.
- The biggest breaking assumption is the current behavior that always forces requests onto the default model. The new requirement explicitly replaces that with model-aware routing.
- If auth, model cache, retry, and circuit breaker are not separated per provider, the system will remain hard to extend and will be prone to cross-provider failures.
