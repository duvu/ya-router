# PRD: Enforce using configured default model for all requests

## Project Context — overview and request
- Request: Ensure the service always uses the configured default model value, ignoring any client-supplied model identifier.
- Motivation: Prevent model selection bypass and ensure predictable billing, feature availability and compatibility with allowed/default model policy.

## Scope
In-scope:
- Behavior specification for server-side routing/selection so that incoming requests that specify a different model still execute using the configured default model.
- Tests and documentation updates to assert the enforced behavior.

Out-of-scope:
- Changing client SDKs to remove model field.
- Changing provider-side model availability beyond configuration (handled separately).

## Acceptance — measurable criteria
1. All API request paths that accept a "model" parameter must execute using the configured default model value.
   - Verified by integration tests that call the same endpoint with different model values and assert returned / used model == configured default.
2. When config defines default_model (example: "gpt-5-mini"), server behavior uses that value regardless of input.
   - Verified by unit/integration tests and CI.
3. Config migration and defaults must continue to set default to "gpt-5-mini" when missing.
   - Verified by existing tests: see [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:24-28) and [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:64-66).

## What existed and what is not (references)
- Default-setting code:
  - Default model assignment: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:107-113) (function `setDefaultModels` sets DefaultModel = "gpt-5-mini" when empty).
  - Loading defaults from example: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:174-206) (function `loadDefaultConfigFromExample` applies `setDefaultModels`).
  - Migration behavior: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:208-246) (function `mergeConfigs` and migration modes).
- Fallback models and runtime model list:
  - Hardcoded fallback list that includes "gpt-5-mini": [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:164-175) (`getDefaultModels()`).
  - Models loading + ultimate fallback usage: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:210-222) and caching flow [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:192-230).
- Configuration example:
  - Example config showing default model: [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:1-8) ("default_model": "gpt-5-mini").
- Tests already asserting default:
  - Tests that expect DefaultModel == "gpt-5-mini": [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:64-66) and integration migration test assertions [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:254-256).

## Easy parts and Hard parts (work breakdown)
Easy (low effort, 0.5–2h):
- Add one or two unit tests to assert that request handling uses cfg.DefaultModel regardless of request payload (e.g., mock request with "model": "something-else").
- Update README to document enforced behavior and config keys: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:311-321).

Medium (1–2d):
- Add/adjust integration tests that exercise the proxy/path which forwards to provider using selected model to observe effective model used.
- Add CI checks asserting GET /v1/models includes default model if applicable (refer to prior PRD in repo): [`github-copilot-svcs/docs/20250824-0804-add-gpt-5-mini-default-PRD.md`](github-copilot-svcs/docs/20250824-0804-add-gpt-5-mini-default-PRD.md:73-77).

Hard (2–5d / higher risk):
- If code paths are diverse (multiple endpoints selecting models), coordinate refactor to centralize model selection to a single utility function to avoid missed paths.
- Audit all request handlers for any direct use of request-supplied model values and replace with centralized enforcement.

## Consistency & Cleanup
- Confirm all usages of model selection are centralized or documented. Candidate files to check:
  - model list and filter logic: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:132-162).
  - config defaults and migration: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:107-113), [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:208-246).
- Add tests (unit + integration) around migration flows to ensure DefaultModel remains enforced: see existing tests [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:180-178) and integration test entries [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:234-256).
- Remove any dead or duplicated model-selection logic discovered during audit; document locations and rationale in follow-ups.

## Decisions (MCP sequentialthinking summary)
- Plan: Enforce server-side canonical source-of-truth for model selection using cfg.DefaultModel; do not honor client-supplied model overrides.
- Expand: Identify all handlers and middleware that currently read a "model" value from requests; create a central enforcement point (utility function/middleware) to return cfg.DefaultModel instead.
- Verify: Use unit tests and integration tests to confirm behavior; rely on existing config defaults and tests that already assume "gpt-5-mini" as default (see test assertions in [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:64-66) and [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:254-256)).
- Reflect: Centralizing model selection reduces risk of divergence and ensures predictable behavior; tradeoff is a small refactor and test additions.

Note: This Decisions subsection records the MCP thinking used to create this PRD. No code modifications are made here; technical follow-ups below request explicit permission before any code edits.

## Minimal code snippet
- No code snippet included by default per requirements. Implementation should be done by adding a single centralized selection function that returns cfg.DefaultModel. (Code changes must be proposed and approved before editing source.)

## Follow-ups — actionable items (no TASKS.yaml)
- Follow-up-001: Audit handlers for direct model usage
  owner: backend@yourteam
  rationale: Ensure no handler bypasses default model enforcement
  priority: high
  acceptance_criteria:
    - List of files referencing request-supplied model produced
    - Centralized enforcement point identified (utility/middleware)

- Follow-up-002: Add unit tests asserting default-model enforcement
  owner: backend@yourteam
  rationale: Prevent regressions; tests should simulate requests with varying "model" payloads
  priority: high
  acceptance_criteria:
    - New unit tests added and passing on CI
    - Tests assert used model equals configured default (e.g., "gpt-5-mini")

- Follow-up-003: Add integration test and CI check
  owner: qa@yourteam
  rationale: End-to-end validation that enforced default is used when forwarding to provider
  priority: medium
  acceptance_criteria:
    - Integration test in CI that sends requests with differing model values and asserts provider receives configured default

- Follow-up-004: README and docs update
  owner: docs@yourteam
  rationale: Make behavior explicit for integrators
  priority: low
  acceptance_criteria:
    - README section updated describing default_model behavior and how to change it in `config.example.json` (`github-copilot-svcs/config.example.json`:1-8)

## Reviewer checklist (validate before closing)
- [ ] Project Context verified
- [ ] Scope defined and agreed
- [ ] Acceptance criteria are testable
- [ ] What-existed references are cited with file+line
- [ ] Consistency & Cleanup items recorded
- [ ] MCP decisions recorded in this document
- [ ] Follow-ups created with owners and priority

## Sources and evidence (explicit file citations)
- Default model assignment: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:107-113)
- Config.example showing default: [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:1-8)
- Fallback runtime model list: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:164-175)
- Models fetch + fallback logic: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:192-230)
- Model filtering behavior: [`github-copilot-svcs/src/models.go`](github-copilot-svcs/src/models.go:132-161)
- Tests asserting defaults: [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:64-66), [`github-copilot-svcs/src/config_test.go`](github-copilot-svcs/src/config_test.go:254-256)
