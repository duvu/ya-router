# PRD: Move Go sources to src/ and add safe config override/migration

## Project Context — why this request
The repository `github-copilot-svcs` currently keeps Go sources at repository root and stores runtime configuration in the user's home directory at `~/.local/share/github-copilot-svcs/config.json`. The user requested developer guidance to:
- Move all Go source files (*.go) into a top-level `src/` directory (user typed "scr/" — clarified below).
- Update code so the application can perform a safe config migration/override of an existing installation's `config.json` (so a new deployment can update config values while preserving sensitive fields such as tokens).

This document gives step-by-step developer guidance, file citations, risks, verification steps and follow-ups needed to implement these changes without performing any source edits here.

## Scope
In-scope:
- Documented, verifiable developer steps to:
  - Move all .go sources into `src/` (not `scr/` — recommendation: use `src/`).
  - Update code to implement a safe config migration/override on start (merge with existing `config.json` rather than blind overwrite).
  - Update build and README instructions, Docker mounts and CI to account for moved sources.
  - Provide suggested tests and verification steps.

Out-of-scope:
- Making the code changes in the repository (no source modifications will be made by this PRD).
- Changing runtime container orchestration beyond instructions to update mounts or image build commands.
- Changing user-facing configuration semantics beyond migration/override behavior described here.

## Acceptance — measurable criteria
1. Build: go build succeeds from repository root using existing Makefile or instructions after moving sources to `src/` (CI pipeline updated accordingly).
2. Run: Application runs and responds on expected port (7071 default) after build/run.
3. Migration behavior:
   - Deploying new binary with migration enabled will update `~/.local/share/github-copilot-svcs/config.json` so new fields from shipped `config.example.json` are present.
   - Existing sensitive fields (example: `copilot_token`, `github_token`, `expires_at`) are preserved unless an explicit override flag is used.
   - Operation is atomic and leaves `config.json` valid JSON; no data loss of existing tokens.
4. Tests:
   - Integration test demonstrates: (a) old config with tokens remains valid after migration, (b) new defaults appear, (c) explicit override flag replaces values.
5. Documentation: README and docker-compose/docs clearly updated to reflect `src/` layout and migration instruction.

## What existed and what is not (evidence, files & line refs)
- Config handling implementations:
  - [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:43) — getConfigPath() (lines 43–52)
  - [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:55) — loadConfig() (lines 55–88)
  - [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:90) — setDefaultModels (lines 90–98)
  - [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:134) — saveConfig() (lines 134–156)
- Runtime CLI and server boot:
  - [`github-copilot-svcs/cli.go`](github-copilot-svcs/cli.go:121) — handleRun() (lines 121–177)
  - CLI commands (status/config/auth): [`github-copilot-svcs/cli.go`](github-copilot-svcs/cli.go:28) (lines 28–44) and [`github-copilot-svcs/cli.go`](github-copilot-svcs/cli.go:97) (lines 97–115)
- Models & fallback behavior:
  - [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:132) — filterAllowedModels (lines 132–162)
  - [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:164) — getDefaultModels (lines 164–184)
  - modelsHandler and caching: [`github-copilot-svcs/models.go`](github-copilot-svcs/models.go:187) (lines 187–241)
- User docs and project layout (to update):
  - README config persistence and example config: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:119) (lines 119–121), example config JSON at [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:245) (lines 245–265).
  - Project structure list: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:406) (lines 406–416).
- Docker config mount note:
  - [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:115) (lines 105–117) — docker run / docker-compose mount examples.

What is not present:
- No existing explicit "config migration" function. Current behavior: loadConfig() loads file if exists, else uses defaults and saveConfig() writes atomically when called. No automatic merging of new defaults into existing configs is present (developer should add merge-on-start logic).

## Easy parts and Hard parts (work breakdown + estimates)
Easy / Low effort (0.5–2h)
- Create `src/` directory and move .go files. Update README instructions and short developer notes.
- Update Makefile and/or CI build commands to `cd src && go build` or use `go` build flags.
- Update docker image build context or Dockerfile COPY paths.

Medium effort (2–6h)
- Implement idempotent config migration/override logic in `config.go`:
  - Add function to compute merged config: read existing config, parse shipped defaults (from `config.example.json` or embedded defaults), copy missing fields into existing config while preserving tokens unless explicit override flag is set.
  - Add CLI flag (example: `--migrate-config=[merge|override|none]`) and document behavior.
- Add integration tests to validate migration semantics (preserve tokens, update new fields).

Hard / Risky (6–16h)
- Update import paths/build system if project uses module paths that assume root package layout — ensure `go.mod` and package main remain valid after move.
- Update container entrypoints and Dockerfile to build/run from new path and preserve volume mounts.
- Ensure CI and release scripts (make, go build commands) are updated and tested across platforms.
- Communicate migration to production operators (documented steps and rollback plan).

## Decisions — MCP sequentialthinking summary (plan, expand, verify, execute, reflect)
- Plan:
  - Move sources into `src/` for clearer repo layout.
  - Implement a config migration/merge that preserves user tokens unless override is explicit.
  - Provide a CLI flag to control migration behavior.
- Expand:
  - Required code points: modify loadConfig/saveConfig to support merge; add new function mergeConfig(existing *Config, defaults *Config, override bool) that returns merged config.
  - Add a code-path on startup (in `handleRun()` before server start) that triggers migration when flag present.
  - Update README and docker instructions to mention migration behavior and flags.
- Verify:
  - Tests: unit tests for merge logic; CI integration test that simulates an existing `config.json` with tokens.
  - Manual verification: run container with mounted `./config` containing existing tokens; start new binary with migration set to `merge` and validate tokens preserved and new defaults appended.
- Execute:
  - This PRD instructs developers how to implement the above. No code execution performed by x51Cto.
- Reflect:
  - Ambiguities addressed: user typed "scr/" — we recommend `src/` as conventional. User typed "configu.json" — assumed `config.json`. These should be confirmed before coding.
  - Risk: migration logic must never erase or overwrite tokens by default. Must be tested thoroughly. See Follow-ups.

(Reference: sequentialthinking plan executed during analysis and recorded here.)

## Minimal code_snippet
Not included. This document intentionally avoids providing full source edits. Developers should implement changes described in "Follow-ups" below. If you want, I can prepare precise patch diffs for code and README edits (requires explicit approval to modify source).

## Consistency & Cleanup
- Update project structure listing in [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:406) (lines 406–416) to reflect `src/` layout.
- Ensure Dockerfile and docker-compose (if present) keep original config mount mapping: check README docker run example [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:105) (lines 105–117) and adjust build context if Dockerfile paths change.
- Remove any root-level `.go` references in Makefile, CI workflows and replace with `src/` paths.
- Consider consolidating configuration defaults: prefer a single source-of-truth (`config.example.json`) and optionally embed defaults in code to avoid desync.

## Follow-ups — actionable items (owner suggestions & priority)
- Follow-up-001: Confirm naming choices and ambiguity
  owner: repository maintainer
  rationale: The user wrote "scr/" and "configu.json" — confirm intended folder name and filename before edits.
  priority: high
  acceptance_criteria:
    - Confirmed folder name (`src/` vs `scr/`)
    - Confirmed config filename (`config.json`)

- Follow-up-002: Move Go sources to `src/` and update build/CI
  owner: developer
  rationale: Move files and update build instructions, Makefile, and CI. Ensure `go.mod` and package main are still valid.
  priority: high
  acceptance_criteria:
    - All .go files moved into `src/`
    - `go build` works from repo root (updated Makefile/CI)
    - README updated: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:406)

- Follow-up-003: Implement safe config migration in code
  owner: developer
  rationale: Add merge-on-start behavior with flag to override. Use atomic writes (saveConfig already writes atomically).
  priority: high
  acceptance_criteria:
    - New function (e.g., mergeConfig) implemented and unit-tested
    - CLI flag added (e.g., `--config-migrate=merge|override|none`)
    - Integration test shows tokens preserved under `merge` and replaced under `override`
    - Relevant code locations to update: [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:55) (loadConfig), [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:134) (saveConfig), and startup flow [`github-copilot-svcs/cli.go`](github-copilot-svcs/cli.go:121) (handleRun)

- Follow-up-004: Update README and rollout notes
  owner: docs maintainer
  rationale: Document migration behavior, CLI flag, and recommended rollout steps for operators.
  priority: medium
  acceptance_criteria:
    - README includes migration instructions and sample commands
    - Release notes document migration impact and rollback steps

- Follow-up-005: CI/integration tests and container validation
  owner: QA engineer
  rationale: Validate container volume mounts and migration in Docker setup. Confirm mount path: `./config:/home/appuser/.local/share/github-copilot-svcs` (`README` lines 115–116).
  priority: medium
  acceptance_criteria:
    - Test covers docker-run + mounted config with tokens preserved after migration
    - CI passes on cross-platform build

## Recommended implementation checklist (developer steps)
1. Confirm naming: `src/` vs `scr/` and config file name.
2. Create `src/` directory and move all .go files there:
   - Files noted in README: `main.go`, `auth.go`, `proxy.go`, `server.go`, `transform.go`, `cli.go`, etc. See [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:406) (lines 406–416).
3. Update `go.mod` or build scripts if package paths require change. Prefer leaving module path unchanged; only move files. Adjust Makefile to run `go build ./src` or `cd src && go build`.
4. Add config migration function:
   - Read existing file via `getConfigPath()` and `loadConfig()` (see [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:43) (lines 43–52), 55–88).
   - Load shipped defaults from `config.example.json` or build defaults (call existing `setDefaultModels()` and `setDefaultTimeouts()`).
   - Merge missing fields from defaults into existing config while preserving `copilot_token`, `github_token`, `expires_at`, `refresh_in` unless `override` chosen.
   - Use `saveConfig()` to write result atomically (see [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:134) (lines 134–156)).
   - Provide CLI flag to control behavior; document in README.
5. Update `handleRun()` to call migration before starting services: [`github-copilot-svcs/cli.go`](github-copilot-svcs/cli.go:121) (lines 121–177).
6. Update README and Docker instructions to reflect new layout and migration behavior (see README lines 119–121, 245–265).
7. Add tests (unit + integration) to verify merge semantics.

## Risks & mitigations
- Risk: Blind override may erase tokens. Mitigation: default to `merge` and preserve token fields; `override` must be explicit via CLI flag.
- Risk: Moving files may break build/CI. Mitigation: update Makefile and CI scripts and run full test suite before merge.
- Risk: Docker build context / container entrypoints referencing old paths. Mitigation: update Dockerfile/compose and test docker-run workflows (see README docker mount lines 115–116).
- Risk: Concurrent startups writing config. Mitigation: reuse existing atomic saveConfig() and document operator guidance (stop old instance before migrating).

## Reviewer checklist
- [ ] Confirm `src/` naming choice and config filename (`config.json`).
- [ ] Verify all referenced file paths and line numbers are accurate: config handling in [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:43,55,90,134).
- [ ] Confirm no source code edits are performed by this PRD (this file is documentation-only).
- [ ] Approve follow-ups for code change implementation before any code is modified.

-- end of document