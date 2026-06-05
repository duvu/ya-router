# Go Runtime Parity Inventory

## External HTTP Contract

- `GET /health`
  - returns JSON with `status`, `service`, and `timestamp`
  - current implementation lives in `src/server.go`
- `GET /v1/models`
  - returns a merged provider model list
  - skips unavailable providers unless `routing.show_unavailable_models` is enabled
  - always keeps `routing.model_map` entries visible even when provider discovery fails
- `POST /v1/chat/completions`
  - OpenAI-compatible surface
  - Copilot chat is a special path: client `model` is ignored and Copilot free-model rotation owns upstream model choice
  - non-Copilot chat resolves through the router by model + capability
- `POST /v1/embeddings`
  - OpenAI-compatible surface
  - resolved through the router by model + capability

## CLI Contract

Current dispatcher in `src/main.go` exposes:

- `help`
- `auth [copilot|codex]`
- `run|start [--config-migrate merge|none|override]`
- `migrate-config --mode merge|override`
- `models [--provider <provider>]`
- `config`
- `status`
- `refresh [--provider <provider>]`
- `version`

Error style is command-specific and exits non-zero from `main.go`, e.g. `Server failed: %v`, `Refresh failed: %v`.

## Runtime/Auth Paths

- Runtime config path: `~/.local/share/github-copilot-svcs/config.json`
- Codex official auth store: `~/.codex/auth.json` or `$CODEX_HOME/auth.json`
- Config writes are atomic and use `0600`
- Codex ChatGPT/device-auth mode treats the official Codex store as primary; config retains mode/enabled state and API-key mode secrets only

## Routing And Provider Rules

- Provider abstraction is defined in `src/provider.go`
- Capabilities are `chat` and `embeddings`
- Router behavior in `src/router.go`
  - first checks `routing.model_map`
  - then provider model discovery
  - explicit model miss for a requested capability is an error
  - only omitted-model requests may fall back to `routing.default_provider`
- `/v1/chat/completions` has a Copilot fast path in `src/proxy.go`
  - if Copilot is registered and implements free-chat proxying, Copilot handles chat before router resolution

## Provider-Specific Behavior

### Copilot

- Device-flow auth in `src/auth.go`
- Provider runtime in `src/copilot_provider.go`
- Supports chat and embeddings
- Chat uses free-model rotation and can shift to the next eligible model before the response is committed
- Embeddings normalize request bodies before upstream proxying

### Codex

- Device auth + refresh in `src/codex_auth.go`
- Provider runtime in `src/codex_provider.go`
- Supports chat and embeddings
- Chat transport splits by auth mode:
  - ChatGPT/device-auth → ChatGPT-backed responses transport
  - API key → Platform API transport
- Embeddings always use the classic platform-style endpoint path in provider logic

## Build/Test/Deploy Assumptions To Preserve

- Go remains the production runtime today
- Current build target: `go build ... ./src`
- Current verification order in repo conventions: `make fmt && make vet && make test && make build`
- CI on `main` still builds/tests Go and triggers container publish + deploy
- `main` push is production-impacting, so Rust work must remain additive until parity/cutover is proven

## Existing Parity-Relevant Tests

- `src/integration_test.go`
  - merged `/v1/models` behavior
  - router resolution behavior
  - config default auth expectations
  - Codex auth-source precedence helpers
- `src/proxy_test.go`
  - Copilot chat fast path
  - Copilot free-model rotation and shift-on-failure behavior
  - request retry/coalescing behavior

## Safest Initial Rust Slice

Before porting live provider behavior, the lowest-risk additive slice is:

1. create a separate Rust workspace under `rust/`
2. define explicit Rust modules for CLI, config, server, routing, transforms, and providers
3. add Rust build/test/fmt/check entrypoints without changing Go build/deploy paths

This keeps production behavior on Go while making the first three OpenSpec tasks measurable.
