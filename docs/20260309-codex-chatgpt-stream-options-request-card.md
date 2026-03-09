# Request Card: Fix Codex ChatGPT Streaming Compatibility and Backend-Specific Request Normalization

Date: 2026-03-09

## 1. Incident Summary

`github-copilot-svcs` is now correctly routing Codex ChatGPT-mode requests to:

`https://chatgpt.com/backend-api/codex/responses`

Authentication is no longer the failing step. The current failure is now:

```text
HTTP 400
{"detail":"Unsupported parameter: stream_options"}
```

This means the request is reaching the correct backend, but the proxy is still forwarding OpenAI-compatible client parameters that the ChatGPT Codex backend does not accept.

This card requests a proper fix at the adapter/transport boundary, not a one-line patch.

## 2. Observed Runtime Evidence

From the production log:

- request enters `POST /v1/chat/completions`
- router resolves model `gpt-5.4` to provider `codex`
- provider uses `mode=chatgpt`
- upstream target is `https://chatgpt.com/backend-api/codex/responses`
- upstream returns `HTTP 400`
- response body is:
  - `{"detail":"Unsupported parameter: stream_options"}`

Important conclusion:

- transport selection is now much closer to correct than before
- the current failure is request-shape incompatibility between the OpenAI-compatible client contract and the ChatGPT Codex backend contract

## 3. Root Cause Analysis

### 3.1 Direct cause

Current request adaptation preserves unsupported client fields and forwards them to the ChatGPT Codex backend.

Relevant code:

- [`src/responses_adapter.go`](../src/responses_adapter.go)
  - `chatToResponsesBody()` preserves unknown keys by default
  - `stream_options` is not dropped or translated
- [`src/responses_adapter.go`](../src/responses_adapter.go)
  - `patchBodyForChatGPT()` currently:
    - forces `stream=true`
    - forces `store=false`
    - removes `max_output_tokens`
  - but it does **not** remove `stream_options`
- [`src/codex_provider.go`](../src/codex_provider.go)
  - ChatGPT mode always converts Chat Completions input into a Responses-style body
  - then applies `patchBodyForChatGPT()`
  - then sends the body directly to `https://chatgpt.com/backend-api/codex/responses`

So the current data flow is:

1. client sends OpenAI-compatible chat request
2. proxy converts it to a generic Responses body
3. proxy forces a few ChatGPT-specific fields
4. proxy still forwards `stream_options`
5. ChatGPT Codex backend rejects the request

### 3.2 Structural cause

The project still uses one generic request-conversion layer for two different upstream contracts:

- Platform Responses API contract
- ChatGPT Codex backend contract

Those contracts are not identical.

The current implementation still assumes:

- most unknown request keys can be safely passed through
- a small patch function is enough to make the body acceptable to ChatGPT mode

The incident shows that assumption is false.

### 3.3 Why a simple global delete is not enough

The naive fix would be:

- delete `stream_options` globally inside `chatToResponsesBody()`

That is not sufficient and may be wrong because:

- `stream_options` is a client-facing OpenAI-compatible parameter
- Platform mode may support it
- ChatGPT mode may not support it upstream, but the proxy may still need its semantic meaning locally
- more backend-specific incompatibilities will likely appear after `stream_options`

This must be solved as a transport-specific compatibility problem, not as a one-off field deletion.

## 4. Secondary Issues Revealed by This Incident

### 4.1 Missing transport-specific allowlist

There is no explicit allowlist for fields accepted by:

- `api.openai.com/v1/responses`
- `chatgpt.com/backend-api/codex/responses`

The code currently relies on:

- a global drop list
- generic pass-through of everything else
- a small ChatGPT patch layer

That pattern is too weak for a proxy that claims OpenAI-compatible input and supports multiple backends.

### 4.2 Client-side semantics are being confused with upstream semantics

`stream_options` is a good example:

- it may be meaningful to the client
- it may be unsupported by the ChatGPT upstream
- therefore the proxy should consume it locally and decide how to shape the downstream response
- it should not automatically be forwarded upstream

### 4.3 Test coverage is missing for this class of bug

Current tests cover:

- generic chat-to-responses conversion
- generic dropped fields
- streaming detection
- JWT account extraction

But they do not cover:

- ChatGPT-specific request sanitization
- removal of upstream-unsupported fields like `stream_options`
- differences between Platform mode and ChatGPT mode
- end-to-end streaming compatibility for ChatGPT mode

## 5. Required Fix

### 5.1 Introduce transport-specific request builders

Replace the current flow:

- `chatToResponsesBody()`
- then `patchBodyForChatGPT()`

with explicit transport-specific builders:

- `buildPlatformResponsesRequest(...)`
- `buildChatGPTCodexRequest(...)`

Requirements:

- both builders start from the same parsed client request
- each builder has its own allowed field set
- each builder owns its own rename/drop/translation rules
- no generic pass-through of unknown fields to upstream in ChatGPT mode

### 5.2 Treat `stream_options` as a client-contract field

For ChatGPT mode:

- parse `stream_options` from the client request
- do not forward it to `chatgpt.com/backend-api/codex/responses`
- preserve its semantic meaning locally if needed

Expected behavior:

- if the client requests streaming, the proxy should decide whether to include usage in the final streamed chat chunk
- this decision should be driven by the client request, not by blindly forwarding `stream_options` upstream

### 5.3 Preserve OpenAI-compatible output behavior

For streaming Chat Completions compatibility:

- if `stream_options.include_usage = true`, include usage in the final emitted chat chunk when usage is available from the upstream `response.completed` event
- if `stream_options` is absent or `include_usage = false`, do not emit usage just because the upstream happened to provide it

The proxy should act as the compatibility layer here.

### 5.4 Keep Platform mode behavior separate

Do not apply the ChatGPT-mode workaround globally.

Requirements:

- ChatGPT mode sanitization must only affect the ChatGPT backend path
- Platform mode must retain its own request shape and capabilities
- the code must make it obvious which fields are supported in which transport

### 5.5 Update stale comments and docs in source

At minimum, review and update:

- [`src/responses_adapter.go`](../src/responses_adapter.go)
- [`src/codex_provider.go`](../src/codex_provider.go)

Reason:

- some comments still describe older assumptions
- request conversion logic is now backend-dependent
- future debugging will be harder if source comments keep describing the wrong contract

## 6. Suggested Implementation Scope

Target files likely involved:

- [`src/responses_adapter.go`](../src/responses_adapter.go)
- [`src/codex_provider.go`](../src/codex_provider.go)
- [`src/responses_adapter_test.go`](../src/responses_adapter_test.go)
- [`src/integration_test.go`](../src/integration_test.go)
- optionally [`src/transform.go`](../src/transform.go) if request parsing is centralized there later

Suggested implementation direction:

1. Parse the incoming OpenAI-compatible request into an internal normalized struct.
2. Separate client-facing fields from upstream-facing fields.
3. Build a ChatGPT Codex request from an explicit allowlist.
4. Build a Platform Responses request from a separate allowlist.
5. Use client-side streaming preferences when shaping the final response to the caller.

## 7. Acceptance Criteria

This request is complete only when all of the following are true:

1. A streaming chat request for model `gpt-5.4` in Codex ChatGPT mode no longer fails with `Unsupported parameter: stream_options`.
2. Requests sent to `https://chatgpt.com/backend-api/codex/responses` do not contain `stream_options`.
3. The fix is transport-specific; Platform mode is not regressed by a global field deletion.
4. Streaming Chat Completions responses remain OpenAI-compatible after the fix.
5. Usage emission in the final streaming chunk follows client intent instead of blindly following upstream presence.
6. Unit tests cover ChatGPT request sanitization and `stream_options` handling.
7. Integration tests cover a streaming ChatGPT-mode request path end to end.

## 8. Required Tests

Add or update tests for at least these cases:

- `buildChatGPTCodexRequest()` strips `stream_options`
- `buildChatGPTCodexRequest()` forces `stream=true` and `store=false`
- `buildChatGPTCodexRequest()` removes other currently known unsupported fields such as `max_output_tokens`
- Platform-mode request building still preserves fields supported there
- streaming response conversion includes usage only when client requested it
- ChatGPT-mode streaming request succeeds through the adapter layer with a mocked upstream
- regression test for the exact incident shape:
  - input contains `"stream": true`
  - input contains `"stream_options": {"include_usage": true}`
  - upstream request body sent by the proxy does not contain `stream_options`

## 9. Non-Goals

This request does not ask for:

- changing routing behavior
- changing authentication flow again
- removing streaming support
- hardcoding special-case fixes directly in `proxy.go`

## 10. Priority

Priority: High

Reason:

- ChatGPT-mode Codex routing is now close to working, but streaming requests still fail for a common OpenAI-compatible client parameter
- a one-off field drop would hide the deeper transport-boundary problem
- this is exactly the class of bug that will keep recurring unless request normalization is split by backend
