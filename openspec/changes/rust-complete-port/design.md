## Context

The service is at a transition point: the Rust workspace (`rust/`) has a compiling skeleton with config parsing, V0→V1 migration, CLI surface, model list, routing resolution, and a sync stdlib HTTP stub. The Go service (`src/`) remains the sole production runtime and is deployed on every `main` merge via CI/Docker/SSH. Completing the port requires replacing the sync stub with an async tokio+axum server, implementing real Copilot and Codex provider auth/request cycles, adding SSE proxy transport, building a parity test harness, running benchmarks, packaging for Docker/CI, and ultimately retiring the Go source.

The "consistency redesign" means: normalize binary naming, promote `rust/` to the primary build target, remove `src/`, and ensure every developer and operator tool (Makefile, Dockerfile, CI, README, AGENTS.md) refers only to the Rust binary. During transition, Go must remain deployable as the fallback until the Rust cutover task explicitly executes.

## Goals / Non-Goals

**Goals:**
- Replace the sync stdlib TCP server with tokio + axum for real concurrent and SSE-capable HTTP handling.
- Implement full Copilot provider: GitHub device-flow auth, token refresh, explicit-model chat dispatch, and embeddings.
- Implement full Codex provider: api_key path (env/config/official store) and ChatGPT/device-code path (official Codex auth store, config fallback), transport selection by mode.
- Implement SSE proxy transport: stream upstream SSE events through to the HTTP client without buffering.
- Build a parity test harness that exercises identical inputs against Go and Rust side-by-side.
- Define and pass concrete benchmark thresholds before Rust becomes the primary runtime.
- Package Rust binary for Docker and CI alongside existing Go build.
- Retire Go source and normalize tooling to Rust-only once cutover task passes gates.
- Ensure operator-visible behavior (config paths, auth stores, CLI flags, endpoint shapes) is unchanged.

**Non-Goals:**
- Adding new providers, models, or upstream APIs not already present in Go.
- Redesigning config schema, auth store locations, or public endpoint contracts.
- Replacing the Go deployment pipeline itself before Rust binary is production-proven.
- Introducing a microservice or multi-binary split; this stays a single binary.

## Decisions

### 1. Async runtime: tokio + axum + reqwest
The sync stdlib TCP server is a placeholder that cannot handle concurrent connections, long-lived SSE streams, or proper HTTP/1.1 keep-alive. Replace it with tokio as the async executor, axum for HTTP routing and response streaming, and reqwest for async upstream proxy requests.

**Why:** tokio + axum is the most widely adopted and documented Rust async HTTP stack. reqwest has built-in streaming body support compatible with SSE proxying. Together they handle the main performance concern (concurrent SSE under load) that motivated the port.

**Alternative considered:** hyper directly without axum. Rejected because axum's router/handler ergonomics reduce boilerplate and the abstraction cost is negligible for this service's size.

### 2. SSE proxy: stream reqwest body bytes to axum response body
For chat completions in streaming mode: detect `"stream": true` in request body, issue reqwest request to upstream, pipe the response body as `text/event-stream` using axum's `Body::from_stream`. For non-streaming: buffer the response and return it as-is.

**Why:** Avoids loading the full SSE response into memory, matching the Go service's streaming copy behavior.

**Alternative considered:** Buffering the full response and re-serializing. Rejected because it breaks streaming UX and increases memory usage.

### 3. Provider auth implemented as owned async structs with a shared `Provider` trait
Define a `Provider` trait with async methods `ensure_authenticated`, `chat_completions`, `embeddings`, `list_models`. Copilot and Codex implement the trait. The router holds `Arc<dyn Provider>` instances.

**Why:** Allows the router and proxy handler to be generic over providers without knowing auth implementation details. Matches the spirit of the existing Go `provider.go` abstraction.

**Alternative considered:** Using `enum Provider { Copilot(..), Codex(..) }`. Rejected because it couples the router to every auth variant and makes adding providers harder.

### 4. Codex auth: implement both api_key and chatgpt modes with official-store priority
Mirror the Go `src/codex_auth.go` / `src/codex_provider.go` credential-source resolution:
- api_key mode: `OPENAI_API_KEY` env → config `api_key` → official store `OPENAI_API_KEY`
- chatgpt mode: official store `~/.codex/auth.json` (or `$CODEX_HOME/auth.json`) → config fallback
Never log raw token/account values; log only boolean metadata.

**Why:** The credential-source ordering is a security contract documented in the existing design and already tested in Go.

### 5. Consistency redesign: Go retired in a final gated task, not incrementally
Keep `src/` intact until a single explicit "cutover" task that: verifies parity tests pass, verifies benchmarks pass, renames Rust binary to `github-copilot-svcs`, updates Dockerfile/CI to build Rust only, removes `src/`, and updates docs.

**Why:** Avoids a broken intermediate state where both runtimes exist without a clear owner. Allows rollback by reverting the cutover task.

### 6. Cargo workspace not needed for a single binary crate
Keep the current `rust/Cargo.toml` as a single crate with lib + bin targets. No workspace split needed until new independent crates emerge.

**Why:** A single crate with modules is simpler and sufficient for the current service size. Adding a workspace layer prematurely increases tooling surface.

## Risks / Trade-offs

- **[Async rewrite risk]** tokio + axum + reqwest introduces several new dependencies. → Minimize dep count; use only what replaces existing responsibility. Review `Cargo.lock` before merge.
- **[Auth regression risk]** Copilot and Codex auth flows involve device flows, OAuth tokens, and file stores. → Add parity tests that run against real auth state in CI before cutover.
- **[SSE proxy fidelity]** Byte-level SSE stream fidelity must be preserved. → Add a test that compares chunk-by-chunk output between Go and Rust proxies against a mock upstream.
- **[Go retirement irreversibility]** Removing `src/` is hard to reverse in production. → Gate retirement strictly behind parity + benchmark pass. Keep full git history.
- **[Benchmark definition]** Workloads must be representative to be meaningful. → Define workloads based on real usage patterns (concurrent streaming chat, model-list aggregation, cold-start latency).

## Migration Plan

1. Add tokio + axum + reqwest to `rust/Cargo.toml`. Rewrite `server.rs` as async. Verify `cargo test` still passes.
2. Implement `Provider` trait. Implement Copilot provider. Implement Codex provider.
3. Implement `proxy.rs` SSE and non-streaming transport.
4. Implement `transforms.rs` request/response normalization.
5. Wire routing + providers + transforms into HTTP handlers in `server.rs`.
6. Build parity test harness. Run side-by-side tests against a mock upstream.
7. Define benchmarks. Run benchmarks. Verify thresholds pass.
8. Add `Dockerfile.rust` and CI job for Rust.
9. Execute cutover task: promote Rust, retire Go, normalize tooling.
10. Update docs, README, AGENTS.md for Rust-only runtime.

## Open Questions

- None blocking — all open questions from the earlier design are resolved by the proposal decisions.
