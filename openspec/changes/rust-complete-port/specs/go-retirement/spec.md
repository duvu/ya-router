## ADDED Requirements

### Requirement: Go source is removed only after parity and benchmark gates pass
The `src/` directory and Go toolchain references SHALL be removed in a single gated cutover task that only executes when all parity tests and benchmark thresholds pass.

#### Scenario: Go source removed atomically with Rust promotion
- **WHEN** the cutover task executes after passing all gates
- **THEN** `src/`, `go.mod`, and `go.sum` (if present) SHALL be removed, the Rust binary SHALL be renamed to `github-copilot-svcs`, and all Makefile/Dockerfile/CI references SHALL point to the Rust build

### Requirement: Operator-facing behavior is unchanged post-retirement
After Go retirement, the Rust binary SHALL accept all existing CLI flags, respond at all existing HTTP endpoints, and use the same runtime config and Codex auth store paths.

#### Scenario: Post-retirement CLI parity
- **WHEN** an operator runs `github-copilot-svcs run`, `auth copilot`, `auth codex`, `status`, `models`, `config`, `refresh`, `migrate-config`, or `version` after retirement
- **THEN** each command SHALL produce behavior equivalent to the corresponding Go command

### Requirement: Rollback plan retains full git history
**BREAKING**: This is a permanent change once executed. Rollback after retirement requires reverting commits via git.

#### Scenario: Rollback path via git revert
- **WHEN** the retirement commit is reverted
- **THEN** the Go source, Makefile Go targets, and Dockerfile SHALL be restored to a deployable state
