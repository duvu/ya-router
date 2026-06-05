## Context

The current service is a single Go binary with flat `src/` layout and mixed responsibilities across CLI dispatch, config migration, provider authentication, model routing, request transformation, upstream proxying, and deployment packaging. The public contract is already broader than just HTTP handlers: local config lives at `~/.local/share/github-copilot-svcs/config.json`, Codex ChatGPT-backed auth depends on `~/.codex/auth.json`, `run` performs config migration automatically, and `main` pushes ship to production through the existing container/deployment pipeline.

A Rust port must therefore preserve behavioral compatibility for HTTP endpoints, CLI verbs, auth/config paths, and deployment/operator workflows while providing a path to improved performance under streaming proxy load, concurrency, and long-lived process operation. The project also currently relies on stdlib-only Go code, so the proposal should assume new Rust dependencies are acceptable only when they replace current runtime responsibilities deliberately and measurably.

## Goals / Non-Goals

**Goals:**
- Preserve the existing OpenAI-compatible HTTP surface and current CLI command set in a Rust implementation.
- Replace the flat Go runtime with a more explicit Rust module/crate structure that separates CLI, config, providers, routing, auth, transforms, and HTTP serving.
- Enable phased migration and parity verification so the Rust implementation can be introduced without a flag day cutover.
- Define measurable performance and resource goals for the Rust port before adoption.
- Keep operator-visible config/auth locations and deployment semantics stable unless explicitly documented otherwise.

**Non-Goals:**
- Redesigning provider behavior, auth semantics, routing policy, or the public API as part of the language port.
- Simultaneously adding new upstream providers or product features unrelated to the port.
- Committing to a full one-step rewrite with no coexistence or parity validation window.
- Preserving the current flat file layout; the Rust port may use a new structure if behavior stays compatible.

## Decisions

### 1. Use behavioral parity, not source translation, as the migration contract
The Rust work should be treated as a behavior-preserving reimplementation rather than a literal line-by-line clone. The compatibility boundary is the external contract: HTTP endpoints, CLI commands, config migration behavior, auth store usage, routing rules, model exposure, and deployment packaging.

**Why:** The current Go code is flat and tightly coupled; translating that layout directly into Rust would copy structural debt without using Rust's strengths.

**Alternative considered:** Direct file-by-file translation. Rejected because it would likely preserve accidental coupling and make performance gains harder to reason about.

### 2. Keep a phased dual-runtime plan until the Rust implementation reaches verified parity
The proposal should assume the Go service remains the reference implementation during migration. The Rust service should be introduced behind separate build/run targets and compared through shared test vectors, smoke tests, and benchmark scenarios before it becomes default.

**Why:** `main` merges deploy to production, and the service has multiple runtime concerns beyond raw request handling. A phased plan reduces cutover risk.

**Alternative considered:** Big-bang replacement of the Go binary. Rejected due to auth/routing/deploy risk.

### 3. Preserve operator-facing paths and workflow semantics first, optimize internals second
The Rust design should keep the current runtime config path, Codex auth store expectations, CLI verbs, and default endpoint behavior. Internal crates/modules can change freely as long as these operational contracts remain stable.

**Why:** Most migration risk is in operational breakage, not compile-time differences.

**Alternative considered:** Redefining config/auth locations during the port. Rejected because it would create avoidable rollout and support churn.

### 4. Define Rust runtime boundaries around current responsibility seams
The proposal should break the service into Rust modules/crates along these boundaries: CLI/entrypoint, config + migration, provider abstraction, Copilot provider, Codex provider, router/model catalog, request/response transforms, HTTP server/proxy transport, and compatibility/perf test harness.

**Why:** These seams match the repo's current behavior boundaries and create a tractable phased migration plan.

**Alternative considered:** A single monolithic Rust crate with no explicit subsystem seams. Rejected because it would reduce maintainability and make parity gaps harder to isolate.

### 5. Performance claims must be backed by explicit benchmark gates
The change should define success criteria such as lower p95/p99 latency under concurrent streaming load, reduced memory growth for sustained SSE proxying, and equal-or-better startup and model-list handling versus Go.

**Why:** “for performance” is the user’s motivation, so the port needs proof rather than assumption.

**Alternative considered:** Treating Rust adoption as self-justifying. Rejected because it would not validate the requested outcome.

## Risks / Trade-offs

- **[Rewrite scope expansion]** → Limit the change to behavior-preserving port work and track unrelated feature requests separately.
- **[Production cutover risk]** → Keep Go as reference until parity and benchmark gates pass; require staged rollout tasks.
- **[Auth/config compatibility regressions]** → Add explicit parity tests for config migration, Codex auth store usage, CLI flows, and status/reporting behavior.
- **[Dependency/operability increase]** → Choose a minimal Rust stack and document every new crate’s responsibility.
- **[Benchmark ambiguity]** → Define workloads and success thresholds before implementation starts.

## Migration Plan

1. Inventory current behavior boundaries and convert them into parity requirements and test vectors.
2. Create Rust workspace/module layout and basic binary skeleton while keeping the Go service unchanged.
3. Port shared config, CLI surface, and provider abstraction first so both runtimes can be compared consistently.
4. Port providers/routing/proxy behavior behind parity tests and benchmark harnesses.
5. Introduce packaging/CI targets for the Rust binary without removing the Go build immediately.
6. Run side-by-side verification and performance comparisons; only then plan default-runtime or deploy cutover.
7. Keep rollback simple by retaining the Go binary and deployment path until Rust readiness is proven.

## Open Questions

- Should the Rust port live in the same repository under a new top-level directory or replace `src/` incrementally after parity is reached?
- Is the target a single Rust binary with integrated CLI/server, or separate binaries sharing common crates?
- Which benchmark workloads best represent real usage here: SSE chat streaming, model-list aggregation, auth refresh, embeddings throughput, or all of them?
- Should the initial Rust target preserve exact config file schema, or is schema compatibility via migration sufficient?
