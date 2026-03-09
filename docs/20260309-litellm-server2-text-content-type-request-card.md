# Request Card: Review and Fix LiteLLM `Invalid value: 'text'` Error for `test-model` in `server2`

Date: 2026-03-09

## 1. Incident Summary

Observed client-side error:

```text
LiteLLM streaming error: 400 litellm.BadRequestError:
OpenAIException - Invalid value: 'text'.
Supported values are: 'input_text', 'input_image', 'output_text',
'refusal', 'input_file', 'computer_screenshot', and 'summary_text'.
Received Model Group=test-model
```

This is no longer the previous `tool_choice` issue. The new failure points to a request-schema mismatch around content-part typing.

The error strongly suggests that somewhere in the `server2` LiteLLM path, a request is being sent in a Responses-style flow while still carrying Chat Completions-style content parts such as:

```json
{"type":"text", "text":"..."}
```

instead of Responses-compatible input parts such as:

```json
{"type":"input_text", "text":"..."}
```

## 2. What This Error Usually Means

The supported values in the error message are Responses API content-part types, not classic Chat Completions content-part types.

That means at least one of these is happening:

1. LiteLLM is routing `test-model` through a Responses-oriented provider path.
2. The upstream behind `test-model` is internally converting Chat Completions requests into Responses requests.
3. The request content is preserved too literally, and `text` parts are not being rewritten to `input_text`.

So this incident should be treated as a schema-translation problem in the deployed request path, not just as a LiteLLM config toggle issue.

## 3. Most Likely Root Cause

### 3.1 End-to-end request-shape mismatch

The most likely end-to-end flow is:

1. the client sends an OpenAI-compatible chat request
2. LiteLLM forwards it to the model group `test-model`
3. `test-model` resolves to an upstream that ultimately uses a Responses-style backend
4. somewhere in that chain, message content items with `type: "text"` are forwarded without conversion
5. the final upstream rejects the payload because it expects `input_text`

### 3.2 This may not be a pure LiteLLM-only bug

If `test-model` points to an upstream proxy such as `github-copilot-svcs` or another OpenAI-compatible adapter, LiteLLM may only be surfacing the error returned by that upstream.

In that case, the deployment review must identify:

- whether LiteLLM itself is transforming the payload incorrectly
- or whether the backend behind `test-model` is doing the wrong Chat-to-Responses conversion

### 3.3 Relevant local code risk in `github-copilot-svcs`

There is already a plausible upstream risk in the local codebase:

- [`src/responses_adapter.go`](../src/responses_adapter.go)
  - `chatToResponsesBody()` preserves non-system message `content` almost verbatim
  - if `content` is an array containing `{ "type": "text", ... }`, it can be forwarded as-is into a Responses-style `input`
  - that would produce exactly the observed upstream error

This does not prove `server2` is targeting this service, but it is a concrete candidate root cause that must be checked during deployment review.

## 4. Required Review Scope for `deployment/server2/`

The review must inspect the real `server2` deployment and answer the following:

### 4.1 Deployment inventory

- which compose file, k8s manifest, or service definition is the real `server2` deployment?
- which LiteLLM config file is actually mounted at runtime?
- which image tag/version of LiteLLM is deployed?
- is the deployed config the same as the version tracked in git?

### 4.2 Model group resolution

- how is `test-model` defined in LiteLLM `model_list`?
- what provider type is associated with it?
- what upstream `model`, `api_base`, and auth mode are configured?
- does `test-model` point directly to OpenAI, to an OpenAI-compatible proxy, or to `github-copilot-svcs`?

### 4.3 Request path contract

- does the client call LiteLLM using `/v1/chat/completions` or `/v1/responses`?
- does LiteLLM convert the request to a Responses-style payload before forwarding?
- does the upstream behind `test-model` do another Chat-to-Responses conversion?
- are content-part types normalized exactly once, or are they being passed through unchanged?

### 4.4 Effective request payload

Capture the real outgoing request body for a failing request and verify:

- whether the final outbound payload contains `type: "text"`
- whether the final outbound payload should instead contain `type: "input_text"`
- whether other content-part types such as images/files are also incorrectly mapped

## 5. Required Fix

### 5.1 Confirm the true normalization boundary

The team must decide where Chat Completions content should be converted into Responses content:

- inside LiteLLM
- inside the upstream proxy behind `test-model`
- or not at all, if the path should stay on Chat Completions end to end

There must be exactly one owner of this translation.

### 5.2 Fix content-part normalization

If `test-model` ultimately uses a Responses-style backend, the path must correctly translate content parts, at minimum:

- `text` → `input_text`
- `image_url` or equivalent image input → `input_image`
- file input → `input_file`

The request path must not send Chat Completions content types into a Responses-style backend.

### 5.3 Review `test-model` provider choice

If `test-model` is defined as a generic OpenAI provider in LiteLLM, verify that this is actually correct.

If the real upstream is:

- a custom OpenAI-compatible proxy
- `github-copilot-svcs`
- a ChatGPT/Codex backend adapter

then the LiteLLM model/provider declaration may need to be adjusted so request validation and request shaping match the real backend contract.

### 5.4 Avoid double adaptation

If LiteLLM already emits Responses-style payloads, the upstream must not re-convert them as if they were Chat Completions payloads.

If the upstream expects Chat Completions input, LiteLLM must not prematurely reshape the request into a Responses payload.

This needs to be explicit in the deployment contract for `test-model`.

### 5.5 Add deployment-level diagnostics

For `server2`, add enough diagnostics to make this class of issue debuggable:

- startup log showing the resolved config path
- startup log showing model-group to upstream mapping
- request debug mode or structured logs showing whether the request path is `chat/completions` or `responses`
- optional redacted payload logging for failing requests in non-production troubleshooting mode

## 6. Acceptance Criteria

This request is complete only when all of the following are true:

1. The real `server2` deployment files and effective LiteLLM config are identified and documented.
2. Requests sent to model group `test-model` no longer fail with `Invalid value: 'text'`.
3. The team can state exactly where Chat-to-Responses content normalization occurs in the `test-model` path.
4. The final outbound payload for Responses-style backends uses `input_text` instead of `text`.
5. The fix does not regress plain text-only chat requests or streaming behavior.
6. If `test-model` points to `github-copilot-svcs` or another upstream adapter, that upstream contract is documented and verified.
7. Logs make it possible to tell which layer performed request translation.

## 7. Required Validation

Validation for the fix must include at least these cases:

- a simple text-only streaming chat request through `test-model`
- a request whose `messages[].content` is an array of content parts
- confirmation that the final outbound payload uses Responses-compatible content types when needed
- confirmation that LiteLLM no longer returns a wrapped `OpenAIException` for `type: "text"`
- regression test for any existing `tool_choice` or `stream_options` workaround already applied in the same deployment

## 8. Deliverables

- reviewed and corrected `server2` LiteLLM deployment config
- documented `test-model` upstream mapping
- clear note describing the request contract between LiteLLM and the upstream behind `test-model`
- smoke-test or regression-test instructions for text-part normalization

## 9. Priority

Priority: High

Reason:

- the failure blocks client requests at the proxy boundary
- the error indicates a deeper contract mismatch, not a superficial unsupported-param toggle
- if left unresolved, similar schema mismatches will keep recurring for images, files, and tool-bearing requests
