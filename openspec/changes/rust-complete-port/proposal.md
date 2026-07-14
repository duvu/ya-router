## Why

The Rust port (OpenSpec change `port-project-to-rust`) completed the workspace scaffold, config/migration parity, CLI surface, HTTP stub server, model list, and routing resolution (tasks 1.1–1.3, 2.1, 2.2). The remaining work — async HTTP serving, Copilot/Codex auth runtimes, SSE streaming proxy, parity tests, benchmarks, Docker/CI packaging, and Go retirement — is substantial enough to warrant a new focused change that defines Rust completion as the primary delivery rather than an additive parallel track.

The user has also requested a consistency redesign: the current `rust/` workspace uses stdlib-only TCP + sync HTTP, which must be replaced with an async Rust runtime (tokio + axum/hyper + reqwest) to actually deliver on the performance motivation. Without this, the Rust port would be slower and less concurrent than the existing Go service for streaming workloads.

## What Changes

- **Replace stdlib sync TCP server in `rust/src/server.rs` with tokio + axum async HTTP runtime** — enables real concurrent request handling and SSE streaming.
- **Add reqwest-based async HTTP client and SSE proxy transport in `rust/src/proxy.rs`** — implements upstream proxying for both streaming and non-streaming chat responses.
- **Implement Rust Copilot provider runtime** in `rust/src/providers/copilot.rs` — GitHub device-flow auth, token refresh, deterministic model routing/selection for chat, and embeddings support.
- **Implement Rust Codex provider runtime** in `rust/src/providers/codex.rs` — api_key path (env/config/official store), ChatGPT/device-code path (official Codex auth store with config fallback), transport selection by auth mode.
- **Implement Rust request/response transform layer** in `rust/src/transforms.rs` — mirrors Go `src/transform.go` normalization.
- **Implement Rust parity test harness** — shared test vectors covering HTTP, routing, config migration, and CLI behavior against Go reference.
- **Define and run benchmarks** — latency/throughput/memory against Go for concurrent SSE chat and model-list workloads.
- **Add Rust Docker/CI packaging** — multi-stage Dockerfile for the Rust binary; Makefile targets for Docker build/push; CI job for Rust alongside the existing Go job.
- **Retire Go source after parity and benchmark gates pass** — `src/` removed, `rust/` promoted to primary, binary name normalized to `github-copilot-svcs`.
- **Consistency redesign** — rename `rust/src/main.rs` binary target to `github-copilot-svcs`, consolidate module structure, update all build/run/deploy references.
- **BREAKING: Primary runtime language changes from Go to Rust** once cutover task is executed.

## Capabilities

### New Capabilities
- `rust-async-runtime`: Tokio + axum HTTP server and reqwest proxy transport replacing sync stdlib TCP implementation.
- `rust-provider-runtime`: Real Copilot and Codex auth/request/response cycles implemented in Rust.
- `rust-parity-validation`: Shared parity test harness and benchmark suite comparing Rust and Go behavior.
- `rust-production-packaging`: Dockerfile, CI job, Makefile targets, and deployment guidance for Rust binary in production.
- `go-retirement`: Removal of Go `src/` after gates pass, promotion of Rust as the sole runtime.

### Modified Capabilities
- `rust-runtime-port`: Async runtime requirement replaces sync stdlib TCP requirement; auth behavior changes from "not implemented" to fully implemented, including deterministic model dispatch.
- `rust-port-validation`: Benchmark workloads and success thresholds become concrete and blocking; parity tests become executable, not just planned.

## Impact

- `rust/Cargo.toml` — adds `tokio`, `axum`, `hyper`, `reqwest`, and `tower` dependencies.
- `rust/src/server.rs`, `rust/src/main.rs` — rewritten for tokio async.
- `rust/src/proxy.rs` — new file implementing SSE/non-streaming upstream proxying.
- `rust/src/providers/copilot.rs`, `rust/src/providers/codex.rs` — full auth/request/response runtime.
- `rust/src/transforms.rs` — full request/response normalization.
- `Makefile` — Rust docker-build, docker-run targets; update help text.
- `Dockerfile.rust` — new file for Rust multi-stage image.
- `.github/workflows/ci-cd.yml` — Rust CI job added; Go CI job preserved until retirement task.
- `src/` — **removed** at the retirement task.
- `README.md`, `docs/` operator docs — updated for Rust primary runtime.
- `AGENTS.md` — updated for Rust build/test/run commands and Go retirement.
