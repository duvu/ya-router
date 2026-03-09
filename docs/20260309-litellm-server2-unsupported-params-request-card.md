# Request Card: Review and Fix LiteLLM `UnsupportedParamsError` for `tool_choice` in `server2`

Date: 2026-03-09

## 1. Incident Summary

Observed client-side error:

```text
LiteLLM streaming error: 400 litellm.UnsupportedParamsError:
openai does not support parameters: ['tool_choice'], for model=gpt-5.4.
To drop these, set litellm.drop_params=True or for proxy:

litellm_settings:
  drop_params: true

If you want to use these params dynamically send allowed_openai_params=['tool_choice'] in your request.
Received Model Group=test-model
```

This indicates the failure is happening at the LiteLLM proxy layer before the request successfully reaches the intended upstream in a compatible shape.

## 2. Current Review Status

### 2.1 Path mismatch found during review

The requested deployment path `deployment/server2/` was not found in the current workspace or under `~/IdeaProjects` during local inspection.

That is itself a deployment review finding:

- the actual `server2` deployment assets need to be located and verified
- any existing operational runbook or docs should point to the real path

This card therefore includes two tracks:

1. locate and review the real `server2` LiteLLM deployment
2. fix the LiteLLM parameter-compatibility issue shown above

### 2.2 Relevant deployment pattern found elsewhere on the machine

A separate LiteLLM deployment found at:

- `/home/beou/IdeaProjects/lucy/lucid-litellm/litellm_config.yaml`

already sets:

```yaml
litellm_settings:
  drop_params: true
```

This is relevant because LiteLLM's own error message recommends exactly that setting for this class of problem.

## 3. Root Cause Hypothesis

Based on the error text, the most likely issue is one or more of the following:

### 3.1 `tool_choice` is being forwarded to a model/provider path that LiteLLM classifies as unsupported

The request arrives at LiteLLM as:

- provider family: `openai`
- model: `gpt-5.4`
- model group alias: `test-model`

LiteLLM then rejects `tool_choice` based on its provider/model compatibility rules.

### 3.2 `drop_params` is missing or ineffective in the real `server2` deployment

If `drop_params: true` is not present in the effective LiteLLM config used by `server2`, LiteLLM will hard-fail on unsupported OpenAI-style params instead of stripping them.

### 3.3 The model alias or provider mapping may be wrong

If `test-model` is intended to target:

- a custom OpenAI-compatible backend
- `github-copilot-svcs`
- Codex ChatGPT mode
- or another non-standard upstream

then configuring LiteLLM as plain `openai` may cause incorrect param validation at the proxy layer.

### 3.4 Deployment drift is possible

Even if the repository config is correct, the live `server2` container may still be using:

- an older config file
- a different mounted file
- a stale image
- or an unpinned `main-latest` image with changed param-support behavior

## 4. Required Review Scope for `server2`

The review must inspect the real deployment under `server2` and answer all of the following:

### 4.1 Deployment inventory

- where is the real `server2` LiteLLM deployment directory?
- which `docker-compose.yml`, `compose.yaml`, Helm values, or systemd units are actually used?
- which LiteLLM config file is mounted into the container?
- is the mounted file the same one tracked in git?

### 4.2 Runtime configuration

- what LiteLLM image tag is deployed?
- is it pinned to a version or using a floating tag?
- what is the effective `litellm_settings` block at runtime?
- is `drop_params: true` enabled in the effective config?
- is there any model-specific override affecting `test-model`?

### 4.3 Model mapping

- how is `test-model` defined in `model_list`?
- what provider type is associated with it?
- what exact upstream model string is used?
- is `gpt-5.4` actually supposed to receive `tool_choice`?
- is the provider declaration correct for the real backend?

### 4.4 Request-path review

- which clients send requests through `test-model`?
- do those clients always send `tool_choice` even when no tool use is needed?
- should LiteLLM drop that field, allow it, or route those calls to a different model/provider?

## 5. Required Fix

### 5.1 Make the deployment robust to unsupported optional params

For the `server2` LiteLLM proxy, the deployment should explicitly choose one of these strategies:

#### Preferred default

Enable:

```yaml
litellm_settings:
  drop_params: true
```

This is the safest default for a shared proxy receiving OpenAI-compatible requests from heterogeneous clients.

#### Alternative only if explicitly required

Allow `tool_choice` for the affected route/model via request or config only if:

- the true upstream supports it
- the provider mapping is correct
- the team wants strict tool-calling behavior preserved end to end

Do not require every client to manually send `allowed_openai_params=['tool_choice']` unless that is a deliberate product decision.

### 5.2 Review provider typing for `test-model`

If `test-model` points to a non-standard backend, review whether LiteLLM should really classify it as `openai`.

The deployment must ensure that:

- provider type matches the actual upstream behavior
- unsupported-param enforcement is not being triggered by the wrong provider adapter

### 5.3 Pin the LiteLLM image version

If `server2` is using a floating tag such as `main-latest`, replace it with a pinned version.

Reason:

- supported-parameter behavior can change between releases
- debugging deployment issues is much harder on moving images

### 5.4 Add deployment-level observability

The deployment should make it easy to confirm:

- which config file was loaded
- which model alias resolved to which upstream
- whether `drop_params` is active
- which params were removed before forwarding

At minimum, startup logs or health/debug output should expose the active model mapping and config path without leaking secrets.

## 6. Acceptance Criteria

This request is complete only when all of the following are true:

1. The actual `server2` LiteLLM deployment path and active config files are identified and documented.
2. Requests sent to model group `test-model` no longer fail with `litellm.UnsupportedParamsError` for `tool_choice`.
3. The effective deployment config clearly shows whether `drop_params` is enabled.
4. The fix is applied at the proxy/deployment layer, not by requiring all clients to change their requests.
5. The LiteLLM image tag used in `server2` is pinned and documented.
6. The mapping from `test-model` to its upstream provider/model is documented and verified.
7. Logs make it possible to tell whether params were dropped or forwarded.

## 7. Required Tests and Validation

Validation for the fix must include:

- a request to `test-model` with `tool_choice` present
- streaming mode enabled
- confirmation that LiteLLM no longer returns `UnsupportedParamsError`
- verification that the upstream still responds correctly after the param is dropped or explicitly allowed
- confirmation that unrelated model groups are not regressed

If possible, add an automated smoke test that runs against the deployed LiteLLM config and asserts:

- `test-model` accepts a request containing `tool_choice`
- the proxy returns a normal upstream response instead of a LiteLLM 400

## 8. Deliverables

- corrected `server2` LiteLLM deployment config
- pinned image/version reference
- short deployment note documenting the real `server2` path and active config files
- regression or smoke-test instructions for `tool_choice` requests

## 9. Priority

Priority: High

Reason:

- the failure blocks streaming requests from at least one client integration
- the error is happening at the proxy boundary, so all clients behind that deployment are exposed
- the deployment path itself is currently unclear, which increases operational risk
