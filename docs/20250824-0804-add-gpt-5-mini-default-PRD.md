# PRD: Add gpt-5-mini to allowed models and make it default

## Project Context Overview
- Request: add gpt-5-mini to allowance models and set as default for github-copilot-svcs.
- Relevant files discovered:
  - [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:3) (allowed_models + default_model)
  - [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:132) (filterAllowedModels, getDefaultModels, modelsHandler)
  - [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:311) (Model Mapping & Available Models)
  - [`github-copilot-svcs/docker-compose.yml`](github-copilot-svcs/docker-compose.yml:11) (config volume mount)
- MCP sequentialthinking used; decisions recorded below.

## Scope
- In-scope:
  - Update documentation and example config to include `gpt-5-mini`.
  - Guidance for setting `gpt-5-mini` as `default_model`.
  - Verification steps and rollout notes.
- Out-of-scope:
  - Modifying application source code without explicit permission.
  - Automated CI changes or runtime provider-side changes.

## Acceptance
- AC-1: `github-copilot-svcs/config.example.json` updated to include `"gpt-5-mini"` in `allowed_models` and `default_model` set to `"gpt-5-mini"`. (See evidence link.)
- AC-2: README updated with model mapping mentioning `gpt-5-mini`.
- AC-3: After deployment, GET /v1/models returns an entry with ID `gpt-5-mini` and POST /v1/chat/completions using model `gpt-5-mini` returns success (200) and generates a response within timeout.
- AC-4: Follow-up items recorded with owners and priorities.

## What existed and what is not
- Existing artifacts:
  - `allowed_models` and `default_model` in [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:3-7).
  - Model filtering and fallback logic in [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:132-162,165-184,186-240).
  - Mapping and docs in [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:311-327,189-193).
  - Docker compose mounts config at [`github-copilot-svcs/docker-compose.yml`](github-copilot-svcs/docker-compose.yml:11-15).
- Missing / gaps:
  - No guarantee that provider model ID matches `gpt-5-mini` (may be vendor-specific).
  - No CI test asserting presence of new model.

## Easy parts and Hard parts
- Easy (low effort):
  - Edit [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:3-7) to add `"gpt-5-mini"` and set `default_model`. (Estimated 10-20m)
  - Update README mapping lines. (Estimated 10-20m)
- Hard (medium effort / verification risk):
  - Verify provider uses `gpt-5-mini` model ID; update `getDefaultModels()` in [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:164-184) if needed. (1-2h)
  - Add CI test to confirm model list includes `gpt-5-mini` (2-4h).

## Decisions
- MCP SequentialThinking summary:
  - Plan: Update sample config + docs, verify model availability, and set default.
  - Expand: Evidence shows config keys at [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:3-7) and runtime filtering in [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:132-162).
  - Verify: Risk that provider uses different ID; recommend verifying via `models.dev` or provider API before rollout.
  - Execute: Document changes and verification steps; do not change core code without approval.
  - Reflect: Record follow-ups for CI, provider verification, and possible code update to `getDefaultModels()`.
- Alternatives considered:
  - Add only to docs vs edit code fallback list — chosen: update docs/config and optionally update `getDefaultModels()` after verification.
- Rationale: Minimal change path preserves runtime behavior while enabling developers to select `gpt-5-mini`.

## Code_snippet
- No code snippet included by request policy. See "What existed" for exact file keys and lines to change.

## Verification & How-to (developer steps)
1. Edit sample config:
   - Update [`github-copilot-svcs/config.example.json`](github-copilot-svcs/config.example.json:3-7): add "gpt-5-mini" to `allowed_models` array and set `default_model` to "gpt-5-mini".
2. Update docs:
   - Add entry for `gpt-5-mini` in [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:311-327).
3. Optional code update (only with approval):
   - Add `"gpt-5-mini"` to `getDefaultModels()` in [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:164-184) to appear in fallback list.
4. Run locally:
   - Start service (make run or docker-compose up).
   - GET http://localhost:7071/v1/models and confirm `gpt-5-mini` present.
   - POST a minimal chat completion request with `"model":"gpt-5-mini"` and confirm success within configured timeouts.
5. Rollout:
   - Update production config file at the host path mounted in [`github-copilot-svcs/docker-compose.yml`](github-copilot-svcs/docker-compose.yml:11).

## Consistency & Cleanup
- Review `getDefaultModels()` in [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:164-184) to ensure new model appears in fallback list if needed.
- Add CI test in repository's test suite to assert GET /v1/models includes `gpt-5-mini` after auth setup.
- Update README table rows under "Model Mapping" [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:311-321).

## Follow-ups
- Follow-up-001: Verify exact provider model ID for GPT-5 Mini
  owner: ops@example.com
  rationale: Upstream provider may use different model ID; confirm canonical ID before changing default.
  priority: high
  acceptance_criteria:
    - Confirmation of provider model ID (from models.dev or provider API)
- Follow-up-002: Add CI test to assert model presence
  owner: qa@example.com
  priority: medium
  acceptance_criteria:
    - CI job fails if GET /v1/models does not return `gpt-5-mini`
- Follow-up-003: Optional code change to add fallback model
  owner: dev@example.com
  priority: low
  acceptance_criteria:
    - PR updating [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:164-184) submitted and approved

## Reviewer checklist
- [ ] Project Context verified (files and lines cited)
- [ ] Scope correct
- [ ] Acceptance criteria measurable
- [ ] Decisions recorded (MCP)
- [ ] Consistency & Cleanup items present
- [ ] Follow-ups recorded with owners and priorities