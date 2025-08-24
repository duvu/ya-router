# PRD: Config migration at startup & project consistency

## Project Context Overview
- Repository: `github-copilot-svcs`
- Goal: ensure application runs a safe configuration migration at startup (default: merge) to avoid outdated/deprecated configuration and enforce repository-wide consistency (source layout, config locations, Docker/CI).
- Evidence:
  - migrateConfig implementation referenced at [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:242).
  - CLI flags and startup hooks referencing migration at [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:134) and [`github-copilot-svcs/src/main.go`](github-copilot-svcs/src/main.go:46).
  - README documents migration and flags: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:278).
  - Existing PRD and docs about moving sources and migration: [`github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md`](github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md:3).

## Scope
- In-scope:
  - Add/verify startup path that runs config migration before server start.
  - Define default migration mode (merge) and ensure tokens are preserved unless override.
  - Update CLI, README, Docker, and CI docs to reflect migration behavior.
  - Implement tests (unit and integration) for migration semantics and Docker-run scenarios.
  - Enforce consistency: source layout (src/), config path and naming conventions, Makefile and Docker build contexts.
- Out-of-scope:
  - Direct modification of application source code in this PRD process (developer work to implement).
  - Changes to orchestration beyond documented guidance (no cluster rollout changes).

## Acceptance — measurable criteria
1. Startup: when binary is started with migration mode set to "merge" (or default), `migrateConfig` is invoked before services start; verify by instrumenting logs or test harness.
2. Preserve tokens: after running in `merge` mode, sensitive fields (`copilot_token`, `github_token`, `expires_at`, `refresh_in`) remain unchanged. Evidence functions: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:242).
3. Atomic write: migrated `config.json` is a valid JSON and written atomically using existing `saveConfig()` semantics at [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:134).
4. CLI: flags accepted `--config-migrate=[none|merge|override]` and documented in [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:17).
5. Docker/CI: docker-compose and Dockerfile instructions updated; integration-test must confirm `./config` volume mount behavior as documented in README (`github-copilot-svcs/README.md` lines ~115).
6. Tests: unit tests for merge logic and integration test that mounts an existing config containing tokens and verifies preservation under `merge`.

## What existed and what is not
- Existing artifacts:
  - `migrateConfig` function: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:242-280).
  - CLI handling and flags: [`github-copilot-svcs/src/main.go`](github-copilot-svcs/src/main.go:29-62) and [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:17-38).
  - README migration docs and examples: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:278-288).
  - Existing documentation PRD for moving sources and migration: [`github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md`](github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md:1-2).
- Missing / gaps:
  - No automatic startup invocation that guarantees migration runs for all deploy modes (needs unified call path in `handleRun()` before server start) — see [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:121).
  - Merge semantics need explicit unit tests and clear merge rules (which fields are preserved and which are overwritten).
  - CI integration tests for Docker-mounted config are not present.

## Easy parts and Hard parts
- Easy (0.5–2h)
  - Update README to call out migration modes and sample commands (see README lines referencing `migrate-config`) [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:283).
  - Add CLI help text and document default mode in README and Docker instructions [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:22).
- Medium (2–6h)
  - Implement a startup hook in `handleRun()` to call `migrateConfig()` when configured; add unit tests for merge semantics and token preservation.
  - Update Dockerfile and docker-compose to mount config path and document in README; validate volume path used in examples.
- Hard / Risky (6–16h)
  - Ensure idempotency across restarts and atomic writes while avoiding token loss. Add integration tests that run container with mounted config and validate behavior.
  - If moving sources to `src/`, update build/CI (Makefile, go build paths) and validate cross-platform builds.

## Decisions — MCP sequentialthinking summary
- Plan:
  - Run safe config migration at startup; default behavior `merge` to append new defaults while preserving tokens.
  - Provide CLI flag to choose `none|merge|override`.
  - Maintain repository consistency: prefer `src/` for Go sources and standard config location (`~/.local/share/github-copilot-svcs/config.json`).
- Expand:
  - Required code touchpoints:
    - Startup path: ensure `handleRun()` calls migration before starting services: see [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:121).
    - Migration implementation: `migrateConfig(mode)` in [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:242-280).
    - Load/save helpers: `loadConfig()/saveConfig()` in [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:43-88) and [`github-copilot-svcs/config.go`](github-copilot-svcs/config.go:134).
    - CLI options parsing: [`github-copilot-svcs/src/main.go`](github-copilot-svcs/src/main.go:29-62).
  - Docs to update: README (`github-copilot-svcs/README.md`), Dockerfile, docker-compose docs, and existing PRD.
- Verify:
  - Define tests: unit tests for merge logic; integration test that runs container with mounted config and asserts preservation of tokens and addition of new defaults.
  - Manual verification: backup config, run binary with migration set to `merge`, inspect resulting config file.
- Execute:
  - This PRD instructs developers; no source edits performed by x51Cto.
- Reflect:
  - Risk: accidental override of tokens if `override` is used incorrectly. Make `override` explicit and documented with warnings.
  - Risk: concurrent startups and race conditions when multiple processes may write config; recommend file locking or atomic rename patterns.

## Consistency & Cleanup
- Standardize locations and names:
  - Enforce `src/` for Go sources and update Makefile to build from `./src` if moved. Reference: [`github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md`](github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md:74).
  - Confirm config path constants at [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:47-50) (config dir and file name).
- Remove duplication:
  - Search for duplicate config parsing logic and consolidate into `loadConfig()`/`saveConfig()` helpers.
- Tests & CI:
  - Add unit tests for merge/override behavior and add a CI pipeline job that runs the Docker integration test.

## Follow-ups
- Follow-up-001: Implement startup migration call
  owner: backend-dev
  rationale: Ensure migrateConfig is executed before services start to avoid old config usage.
  priority: high
  acceptance_criteria:
    - `handleRun()` invokes `migrateConfig()` when migration mode ≠ `none`. Verify by code references: [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:121).
- Follow-up-002: Add merge semantics tests
  owner: backend-dev / QA
  rationale: Prevent regressions and token loss.
  priority: high
  acceptance_criteria:
    - Unit tests for merge function covering preserved token fields and added defaults.
- Follow-up-003: Update README and release notes
  owner: docs maintainer
  priority: medium
  acceptance_criteria:
    - README includes migration instructions and sample commands (`migrate-config` and `--config-migrate` examples).
- Follow-up-004: Add Docker integration test
  owner: QA engineer
  priority: medium
  acceptance_criteria:
    - Container run with mounted `./config` preserves tokens after migration under `merge`.
- Follow-up-005: File-locking review
  owner: backend-dev
  priority: low
  acceptance_criteria:
    - Determine if file locking or atomic write via temp file+rename is needed to avoid races.

## Reviewer checklist
- [ ] Project Context verified (references present)
- [ ] Scope defined (in-scope/out-of-scope)
- [ ] Acceptance criteria measurable and present
- [ ] What existed and gaps documented with file citations
- [ ] Decisions (MCP plan/expand/verify/reflect) recorded
- [ ] Consistency & Cleanup actionable items included
- [ ] Follow-ups recorded with owners and priorities

## Sources / Evidence
- `migrateConfig()` function: [`github-copilot-svcs/src/config.go`](github-copilot-svcs/src/config.go:242-280).
- CLI flags and startup: [`github-copilot-svcs/src/main.go`](github-copilot-svcs/src/main.go:29-62), [`github-copilot-svcs/src/cli.go`](github-copilot-svcs/src/cli.go:17-38).
- README migration docs: [`github-copilot-svcs/README.md`](github-copilot-svcs/README.md:278-288).
- Existing PRD on migration and src/: [`github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md`](github-copilot-svcs/docs/20250824-0823-move-go-src-and-config-migration-PRD.md:3-9).