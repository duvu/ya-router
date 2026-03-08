# Request Card: Rebuild Codex ChatGPT Integration So It Uses the Correct Backend and Auth Contract

Date: 2026-03-08

## 1. Summary

`github-copilot-svcs` currently routes Codex chat traffic to `https://api.openai.com/v1/responses` using a bearer token obtained from the Codex/ChatGPT device-code flow.

That assumption is not holding in production. The observed runtime error is:

```text
401 insufficient permissions
Missing scopes: api.responses.write
```

Official Codex still works for the same user account, which strongly indicates that the problem is not a stale token and not a simple refresh failure. The current `github-copilot-svcs` Codex implementation is using the wrong transport/auth contract for ChatGPT-backed Codex usage.

This card requests a full remediation of the Codex provider. If a clean fix requires replacing most or all of the current Codex implementation, that is in scope.

## 2. Observed Failure

Observed request path:

- Router resolves `gpt-5.4` to provider `codex`
- Provider proxies `POST /v1/chat/completions`
- Provider rewrites the request to `POST https://api.openai.com/v1/responses`
- Upstream returns `401` with `Missing scopes: api.responses.write`
- Proxy retries by reloading the token from disk
- Retry still returns `401`
- Request is logged as `OK` even though the client receives an upstream auth error

This proves two things:

1. The current retry strategy is aimed at token freshness, but the actual failure is missing capability/scope.
2. Observability is misleading because the request log marks the request as successful when the upstream operation failed.

## 3. Root Cause Review

### 3.1 What the local code is doing today

Current code makes the following assumptions:

- [`src/codex_provider.go`](../src/codex_provider.go) hardcodes `codexAPIBase = "https://api.openai.com/v1"`.
- [`src/codex_provider.go`](../src/codex_provider.go) sets `Authorization`, `OpenAI-Beta`, and `OpenAI-Organization` / `OpenAI-Project` headers from JWT claims.
- [`src/codex_provider.go`](../src/codex_provider.go) explicitly states that the Codex `device_code` token is authorized for `/v1/responses`.
- [`src/responses_adapter.go`](../src/responses_adapter.go) is built around that same assumption and converts Chat Completions requests into Responses API requests.
- [`src/codex_auth.go`](../src/codex_auth.go) re-implements the device-code flow and persists access and refresh tokens in the service config, not in the official Codex credential store.
- [`src/cli.go`](../src/cli.go) and [`src/main.go`](../src/main.go) only expose `device_code` for Codex in actual code paths.

These assumptions are not supported by the observed runtime result.

### 3.2 What the official `openai/codex` source shows

The following official sources were reviewed:

- OpenAI Codex root README:
  - <https://raw.githubusercontent.com/openai/codex/main/README.md>
  - Confirms Codex supports both ChatGPT login and API key auth.
- Official device-code login flow:
  - <https://raw.githubusercontent.com/openai/codex/main/codex-rs/login/src/device_code_auth.rs>
  - Confirms device-code login is real and first-party.
- Official ChatGPT integration crate:
  - <https://raw.githubusercontent.com/openai/codex/main/codex-rs/chatgpt/README.md>
  - States this crate pertains to first-party ChatGPT APIs and products such as Codex agent.
- Official ChatGPT client implementation:
  - <https://raw.githubusercontent.com/openai/codex/main/codex-rs/chatgpt/src/chatgpt_client.rs>
  - Uses `config.chatgpt_base_url`
  - Loads token data from the Codex auth manager
  - Sends bearer auth plus `chatgpt-account-id`
- Official ChatGPT token bootstrap:
  - <https://raw.githubusercontent.com/openai/codex/main/codex-rs/chatgpt/src/chatgpt_token.rs>
  - Initializes token data from the official auth manager using `codex_home`
- Official wire-layer API crate:
  - <https://raw.githubusercontent.com/openai/codex/main/codex-rs/codex-api/README.md>
  - States provider configuration owns base URLs, headers, query params, auth injection, retry, and stream behavior

### 3.3 Technical conclusion

Inference from the official sources:

- ChatGPT-backed Codex usage is not just “run device auth, then send the resulting bearer token directly to `https://api.openai.com/v1/responses`”.
- Official Codex has a distinct ChatGPT/Codex backend path and a distinct auth/bootstrap layer.
- Official ChatGPT-backed calls rely on state from the official auth manager and include fields such as `chatgpt-account-id`, which the current project does not model.

Therefore the current `github-copilot-svcs` Codex provider is architecturally incorrect for ChatGPT-backed Codex mode.

## 4. Additional Gaps Found During Review

### 4.1 Code and documentation disagree

README currently claims:

- `auth codex` prefers cached auth under `CODEX_HOME` or `~/.codex`
- otherwise it runs `codex login --device-auth`
- `chatgpt_device_auth` is a supported mode

But actual source currently does this instead:

- runs its own device-code flow
- stores Codex tokens in `github-copilot-svcs` config
- defaults to `device_code`
- does not implement the README contract in the code paths reviewed

This drift must be removed.

### 4.2 Current logs hide the failure severity

- [`src/proxy.go`](../src/proxy.go) logs request completion as `OK` whenever the provider returns `nil`
- [`src/responses_adapter.go`](../src/responses_adapter.go) forwards upstream `401` to the client but returns `nil`

Result: production logs can show `COMPLETED ... OK` for requests that actually failed upstream.

### 4.3 Current auth state is incomplete for official ChatGPT-backed mode

The current config only stores:

- `access_token`
- `refresh_token`
- `expires_at`

But official ChatGPT-backed calls also depend on data such as:

- Codex credential source and storage mode
- `codex_home`
- token data loaded from the official auth manager
- `account_id` used in `chatgpt-account-id`

The current auth model is too small to represent the official flow correctly.

## 5. Required Product Decision

The project must explicitly separate these two Codex modes:

### Mode A: `api_key`

Use OpenAI Platform API semantics.

Allowed behavior:

- call `https://api.openai.com/v1/...`
- use API-key auth
- use Responses API or other public platform endpoints

### Mode B: `chatgpt`

Use official Codex ChatGPT login semantics.

Allowed behavior:

- use the official Codex credential source
- use the backend contract that matches official Codex ChatGPT mode
- do not assume public platform scopes such as `api.responses.write`

The current `device_code` mode is mixing login acquisition with backend transport selection. That needs to be split. Device code is a login method, not proof that the resulting token can be used as a public Platform API token.

## 6. Implementation Request

### 6.1 Phase 0: Root-cause confirmation against official source

Read the official `openai/codex` source in enough depth to document:

- which backend is used for ChatGPT-backed Codex traffic
- which headers are required
- which credential source is authoritative
- how model discovery works in ChatGPT-backed mode
- whether ChatGPT-backed mode and API-key mode share the same wire contract or not

If the answer is materially different from the current implementation, the team is authorized to replace the current Codex provider end to end instead of patching it incrementally.

### 6.2 Phase 1: Replace the current Codex auth abstraction

Add an explicit Codex credential source layer:

- `APIKeyCredentialSource`
- `OfficialCodexAuthSource`

Requirements:

- `OfficialCodexAuthSource` must read from the same official Codex auth substrate used by the real CLI
- it must honor `CODEX_HOME`
- it must not assume that all credentials always live only in plaintext JSON
- it must treat `~/.codex/auth.json` and any equivalent secret store data as secret material

Unless there is a deliberate product decision to do otherwise, `github-copilot-svcs` must stop storing ChatGPT/Codex bearer and refresh tokens in its own `config.json`.

### 6.3 Phase 2: Split the transport layer

Introduce separate transport implementations:

- `CodexPlatformTransport`
- `CodexChatGPTTransport`

Requirements:

- `CodexPlatformTransport` is used only in `api_key` mode
- `CodexChatGPTTransport` is used only in ChatGPT-backed mode
- provider selection must depend on both provider and auth mode
- `codex_provider.go` must stop hardcoding `https://api.openai.com/v1` for every Codex request
- current JWT-derived `OpenAI-Organization` / `OpenAI-Project` header logic must not be used as a substitute for the official ChatGPT-backed contract

### 6.4 Phase 3: Rework Codex CLI behavior

CLI and config need a clean contract:

- `auth codex --api-key`
- `auth codex --chatgpt`
- `auth codex --device-auth`

Requirements:

- `--device-auth` must mean “acquire ChatGPT-backed credentials through the official device-code path”
- auth commands must not imply that ChatGPT login produces a public API token with Responses API write scope
- `status` must show:
  - auth mode
  - credential source
  - whether ChatGPT-backed credentials are present
  - whether required metadata such as account identity is available

### 6.5 Phase 4: Fix request/response semantics and observability

Requirements:

- a request that returns upstream `4xx` must never be logged as `OK`
- the final status code written to the client must be visible in request completion logs
- `401` due to missing scope must be classified separately from token-expired and token-missing cases
- the service must stop retrying token reload for errors that are clearly capability/scope failures
- remove or rewrite misleading comments claiming that a device-code token is authorized for `/v1/responses`

### 6.6 Phase 5: Fix model discovery and routing behavior

Requirements:

- model discovery must be transport-aware
- ChatGPT-backed Codex mode must not rely on unsupported platform model-list calls
- if model availability depends on official backend entitlements, the returned model list must reflect that reality
- hardcoded fallback model lists are acceptable only if explicitly documented as compatibility shims and clearly scoped by mode

## 7. Acceptance Criteria

This request is complete only when all of the following are true:

1. A user who can use official Codex with ChatGPT login can use Codex through `github-copilot-svcs` without supplying `OPENAI_API_KEY`, provided the requested model is supported in the selected mode.
2. ChatGPT-backed Codex mode no longer sends requests to `https://api.openai.com/v1/responses` unless official source verification shows that this is correct for that mode.
3. `api_key` mode remains supported and is the only mode that assumes public Platform API semantics by default.
4. Request logs record the real final HTTP status, including upstream `401`.
5. Scope/capability failures produce explicit diagnostics instead of token-refresh loops.
6. README, config example, CLI help, and implementation all describe the same Codex behavior.
7. Secret material from Codex auth storage is never logged or exposed.
8. Automated tests cover the misrouting case that currently produces `Missing scopes: api.responses.write`.

## 8. Required Tests

Add or update tests for at least these cases:

- ChatGPT-backed Codex mode does not use the Platform Responses endpoint by accident.
- API-key Codex mode does use the Platform endpoint and injects the correct auth headers.
- A `401` with `Missing scopes: api.responses.write` is surfaced as a mode/configuration error, not as a token-refresh problem.
- Request completion logs reflect the actual written status code.
- `status` accurately reports the selected auth mode and credential source.
- Config migration handles new Codex auth fields safely.
- Regression coverage for streaming and non-streaming chat requests in both Codex modes.

## 9. Non-Goals

This request does not authorize:

- cookie scraping
- browser session extraction
- undocumented token harvesting
- exposing `~/.codex/auth.json` contents through logs or APIs

## 10. Suggested Deliverables

- updated architecture note for Codex mode separation
- rewritten Codex provider implementation
- updated CLI and config schema
- updated README and usage guide
- regression tests for ChatGPT-backed and API-key Codex modes
- short migration note for existing users who authenticated with the current broken `device_code` flow

## 11. Priority

Priority: High

Reason:

- current behavior breaks a core advertised feature
- current logs make the failure easy to misdiagnose
- current docs overstate support for a mode that is not correctly implemented
