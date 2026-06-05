## Context

The Go service has two layers that restrict which models are visible to clients:

1. **Provider-level filter (`allowed_models` in config)**: `CopilotProvider.ListModels` and `CodexProvider.ListModels` both call `filterAllowedModels` before returning results. When `AllowedModels` is non-empty the filter silently drops every model not in the list.
2. **Rust workspace (`rust/`)**: A partial Rust port added under `rust/Cargo.toml` with 26 tests, config/CLI/server/routing/models scaffolding, and five `rust-*` Makefile targets. It is not deployed, not integrated into CI, and has no production path.

Constraints: No external Go deps. `main` push deploys Go binary to production. Config schema must stay backward-compatible (no forced migration).

## Goals / Non-Goals

**Goals**
- Providers expose every model they list upstream; `AllowedModels` config has no effect on the `/v1/models` response.
- The `rust/` workspace and Makefile rust-* targets are fully removed.
- Config field `allowed_models` survives in the schema for forward compatibility but is unused for model listing.
- All Go tests pass after both removals.

**Non-Goals**
- Removing the `AllowedModels` field from config structs (keep for future use).
- Removing `filterAllowedModels` / `isModelAllowedWithPrefix` helpers (keep; may serve routing later).
- Touching Dockerfile, CI workflow, or deployment configuration.
- Modifying the `allowed_models` migration path in `migrateConfig`.

## Decisions

**Decision 1: Remove filter call at provider level, not at handler level.**
`filterAllowedModels` is called inside `CopilotProvider.ListModels` and `CodexProvider.ListModels`. Removing the call there is the minimal change — one line per provider — and leaves `modelsHandler` and `filterAllowedModels` untouched.

Alternative considered: Make `filterAllowedModels` a no-op by clearing `AllowedModels` at startup. Rejected: silent config override is confusing and leaves dead config fields appearing to work.

**Decision 2: Keep `AllowedModels` in config schema.**
Config files with existing `allowed_models` entries should parse without error. The field is simply ignored at list time. No migration is triggered.

Alternative considered: Remove the field entirely and add config migration. Rejected: breaking config change with no benefit.

**Decision 3: Delete `rust/` entirely rather than archiving it.**
The Rust workspace is not tracked by git in production (it was never committed to main), so there is no rollback risk on the production side. Deleting it from the worktree is straightforward.

Alternative considered: Move to `_rust_archive/`. Rejected: dead code in the repo adds noise.

**Decision 4: Remove rust-* Makefile targets only, not the full Makefile.**
The Go targets (`build`, `test`, `fmt`, `vet`, etc.) are unchanged. Only the five rust-* additive targets and the corresponding `.PHONY` entries are removed.

## Risks / Trade-offs

- [Risk: Tests that assert `allowed_models` filtering in `ListModels`] → Update those tests to reflect pass-through behaviour. Straightforward.
- [Risk: Operators relying on `allowed_models` for model restriction] → `allowed_models` still parses; operators are not broken. The behaviour change is documented.
- [Risk: Rust workspace removal leaves stale OpenSpec changes] → `port-project-to-rust` and `rust-complete-port` changes are in `openspec/changes/` as documentation. They can remain archived without causing confusion.

## Migration Plan

1. Remove `filterAllowedModels` call from `CopilotProvider.ListModels`.
2. Remove `filterAllowedModels` call from `CodexProvider.ListModels`.
3. Delete `rust/` directory.
4. Remove rust-* targets from `Makefile`.
5. Update tests that checked filtered model lists.
6. Update `config.example.json` and `docs/CONFIGURATION.md` to reflect that `allowed_models` is no longer used for listing.
7. Run `make fmt && make vet && make test && make build`.

Rollback: Revert the two provider file changes. No config migration needed.

## Open Questions

None. Scope is well-defined.
