## 1. Async runtime foundation

- [ ] 1.1 Add `tokio`, `axum`, `hyper`, `reqwest`, and `tower` to `rust/Cargo.toml` with pinned versions
- [ ] 1.2 Rewrite `rust/src/server.rs` with `#[tokio::main]` runtime, axum Router, and async handlers for `/health`, `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`
- [ ] 1.3 Update `rust/src/main.rs` `run` command path to call the async server and confirm `make rust-build && cargo test` pass

## 2. SSE and non-streaming proxy transport

- [ ] 2.1 Create `rust/src/proxy.rs` with `proxy_request(upstream_url, headers, body, stream) -> axum::Response` using reqwest async client
- [ ] 2.2 Implement streaming path: detect `"stream": true` in request body, forward reqwest `Response::bytes_stream()` as axum `Body::from_stream` with `Content-Type: text/event-stream`
- [ ] 2.3 Implement non-streaming path: buffer response body and return as HTTP 200 with original Content-Type
- [ ] 2.4 Add tests for proxy module: streaming body passes through unchanged, non-streaming body is buffered correctly

## 3. Provider trait and Copilot provider

- [ ] 3.1 Define `Provider` trait in `rust/src/providers/mod.rs` with async methods: `ensure_authenticated`, `chat_completions`, `embeddings`, `list_models`
- [ ] 3.2 Implement `CopilotProvider` struct with GitHub device-flow auth using `reqwest` (POST to GitHub OAuth device endpoint)
- [ ] 3.3 Implement Copilot token refresh: store refresh token, detect expiry, refresh before request
- [ ] 3.4 Implement Copilot chat: forward to configured provider for the resolved upstream model and call upstream via `proxy.rs`
- [ ] 3.5 Implement Copilot embeddings: route to upstream embeddings endpoint
- [ ] 3.6 Add unit tests for Copilot routing input validation and auth state transitions

## 4. Codex provider

- [ ] 4.1 Implement `CodexProvider` struct with auth-mode dispatch: `api_key` vs `chatgpt`
- [ ] 4.2 Implement api_key credential resolution: `OPENAI_API_KEY` env → config `api_key` → official store (mirrors `resolveCodexAPIKey` in Go)
- [ ] 4.3 Implement chatgpt credential resolution: official store `$CODEX_HOME/auth.json` → config fallback (mirrors `resolveCodexChatGPTAuth` in Go)
- [ ] 4.4 Implement Codex chat: build request per transport (ChatGPT vs Platform API), route via `proxy.rs`
- [ ] 4.5 Implement Codex model listing: return `allowed_models` from config when provider is enabled
- [ ] 4.6 Verify all Codex auth log lines contain no raw token/account values; add test assertions
- [ ] 4.7 Add unit tests for Codex credential source priority and transport selection

## 5. Request/response transforms

- [ ] 5.1 Implement `rust/src/transforms.rs` with request normalization: strip unsupported fields, normalize `model` field per routing decision
- [ ] 5.2 Implement response normalization: ensure OpenAI-compatible response shape for both streaming and non-streaming
- [ ] 5.3 Wire transforms into HTTP handlers in `server.rs` (request in → normalize → route → proxy → normalize → response out)
- [ ] 5.4 Add transform tests matching Go `src/transform_test.go` behavior

## 6. Router integration with providers

- [ ] 6.1 Update `rust/src/routing.rs` `resolve_model` to accept provider registry (`HashMap<String, Arc<dyn Provider>>`) and validate provider is reachable
- [ ] 6.2 Wire router + providers into axum handlers: resolve route, call provider method, stream/buffer response
- [ ] 6.3 Implement `/v1/models` handler to call `list_models` on all enabled providers and merge results (deduplicating by ID, with model_map visibility)
- [ ] 6.4 Add integration test: `/v1/models` returns merged list with model_map entries visible when provider discovery fails

## 7. CLI auth commands wired to provider runtime

- [ ] 7.1 Wire `auth copilot` CLI command to `CopilotProvider::device_flow_auth()` and save tokens to config
- [ ] 7.2 Wire `auth codex` CLI command to Codex device/chatgpt flow and persist to official store (mirrors `handleAuthCodex` in Go)
- [ ] 7.3 Wire `auth codex --api-key` to config API key storage
- [ ] 7.4 Wire `refresh` command to provider-specific token refresh logic
- [ ] 7.5 Wire `status` command to show resolved credential source and auth state per provider (no raw secrets)
- [ ] 7.6 Add CLI tests for auth error paths and status output format

## 8. Parity test harness

- [ ] 8.1 Create `rust/tests/parity.rs` that starts both Go binary and Rust binary as child processes against mock upstream
- [ ] 8.2 Add parity assertions for: `/health` body, `/v1/models` shape, routing error for unknown model, non-streaming stub response shape
- [ ] 8.3 Confirm parity suite passes with `cargo test --test parity`

## 9. Benchmarks

- [ ] 9.1 Create `rust/benches/proxy_bench.rs` with criterion: concurrent SSE chat proxy benchmark (N=10 concurrent, 100 requests each)
- [ ] 9.2 Add benchmark for `/v1/models` sequential aggregation latency
- [ ] 9.3 Run benchmarks against Go service; record baseline in `rust/benches/baseline.txt`
- [ ] 9.4 Verify Rust meets or beats Go on p95 latency and throughput for both workloads

## 10. Docker and CI packaging

- [ ] 10.1 Create `Dockerfile.rust` with multi-stage build: `rust:1.78-alpine` builder → `alpine:latest` runtime, binary at `/app/github-copilot-svcs-rs`, expose 7071
- [ ] 10.2 Add `rust-docker-build` and `rust-docker-run` Makefile targets
- [ ] 10.3 Add Rust CI job to `.github/workflows/ci-cd.yml`: runs `cargo test` and `cargo build --release` on every push, in parallel with existing Go job

## 11. Go retirement and consistency redesign

- [ ] 11.1 Rename Rust binary target in `rust/Cargo.toml` from `github-copilot-svcs-rs` to `github-copilot-svcs`
- [ ] 11.2 Update `Dockerfile.rust` to produce image tagged as `github-copilot-svcs`; update `docker-compose.yml` to use the Rust image
- [ ] 11.3 Update `.github/workflows/ci-cd.yml` deploy job to build and push the Rust Docker image; remove the Go-only build step
- [ ] 11.4 Remove `src/`, `go.mod`, Go-only Makefile targets, and Go Dockerfile references after parity+benchmark gates pass
- [ ] 11.5 Move `rust/` contents to repo root (or update build roots) so `cargo build` is the default build command
- [ ] 11.6 Update `AGENTS.md`, `README.md`, and `docs/HUONG_DAN_SU_DUNG.md` for Rust-primary runtime, commands, and deploy flow
