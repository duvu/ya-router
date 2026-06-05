## 1. Parity inventory and Rust workspace setup

- [x] 1.1 Inventory the current Go behavior by endpoint, CLI command, config path, auth store usage, routing rule, and deploy/build assumption
- [x] 1.2 Create the Rust workspace/binary layout for CLI, HTTP runtime, providers, routing, transforms, and config handling
- [x] 1.3 Add Rust build/test/lint commands and local developer entrypoints without removing the existing Go workflow

## 2. Core runtime port

- [x] 2.1 Implement Rust config loading and migration behavior compatible with `~/.local/share/github-copilot-svcs/config.json`
- [x] 2.2 Implement the Rust CLI surface for `run`, `auth`, `status`, `config`, `models`, `refresh`, `migrate-config`, and `version`
- [ ] 2.3 Implement Rust provider abstractions plus Copilot and Codex auth/runtime behavior with existing credential-source semantics
- [ ] 2.4 Implement Rust request routing, model aggregation, request/response transforms, and OpenAI-compatible HTTP handlers for `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, and `/health`
- [ ] 2.5 Implement Rust streaming proxy transport behavior for SSE and non-streaming upstream responses

## 3. Validation, packaging, and rollout

- [ ] 3.1 Build parity tests and smoke checks that compare Go and Rust behavior across CLI, auth, routing, and HTTP flows
- [ ] 3.2 Create benchmark workloads and success thresholds for latency, throughput, and resource usage against the current Go runtime
- [ ] 3.3 Add Docker/CI packaging for the Rust runtime while preserving rollback to the Go binary until cutover is approved
- [ ] 3.4 Update operator/developer documentation and deployment guidance for phased Rust adoption and rollback
