## Why

The service currently runs as a flat single-binary Go application with provider auth, routing, request transformation, SSE proxying, and CLI/runtime logic all coupled in `src/`. A Rust port is being requested to improve performance, tighten memory/concurrency behavior under proxy load, and create a maintainable path toward a more explicitly modular runtime without changing the public OpenAI-compatible surface.

## What Changes

- Introduce a Rust implementation of the service that preserves the current OpenAI-compatible HTTP endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, `/health`) and the existing operator-facing CLI responsibilities.
- Define a phased migration path so the Rust implementation can be validated against current Go behavior before any production cutover.
- Define compatibility requirements for config paths, auth storage expectations, routing/model behavior, deploy pipeline assumptions, and operator workflows.
- Define performance-validation requirements so the Rust port has measurable success criteria rather than being a language rewrite by assumption.
- **BREAKING**: the long-term implementation target changes the primary runtime language from Go to Rust, which affects build tooling, project layout, local development flow, and release packaging.

## Capabilities

### New Capabilities
- `rust-runtime-port`: A Rust service implementation that preserves the existing HTTP and CLI behavior contract of the Go service.
- `rust-port-validation`: Compatibility, benchmark, and rollout requirements for proving the Rust port is safe to adopt.

### Modified Capabilities
- None.

## Impact

- Affected code: current `src/` runtime, CLI/auth/config/routing/proxy/model logic, tests, Dockerfile, Makefile, CI workflow, and deployment packaging.
- Affected systems: local config at `~/.local/share/github-copilot-svcs/config.json`, official Codex auth store at `~/.codex/auth.json`, Docker image build/release pipeline, and production deployment triggered from `main`.
- Likely dependencies/systems: Rust toolchain, async HTTP/runtime crates, serialization/config crates, and benchmark/test harness additions for cross-language parity verification.
