## Why

The repository already contains partial Codex support, but the current Codex behavior is still inconsistent with both the repo’s own README contract and the official Codex implementation details captured in `docs/20260308-codex-chatgpt-transport-remediation-request-card.md`. The project needs a verified, provider-safe Codex integration that works beside GitHub Copilot instead of behaving like an incomplete or misleading second backend.

## What Changes

- Complete Codex support as a first-class provider alongside GitHub Copilot.
- Separate Codex `api_key` behavior from ChatGPT-backed/device-auth behavior so auth mode determines the correct credential source and upstream transport.
- Make routing and model exposure reliably multi-provider for chat completions, embeddings, and `/v1/models`.
- Tighten CLI, status, and security behavior so Codex credentials are handled through explicit provider-aware flows and secret material is never exposed.
- Preserve the OpenAI-compatible public API shape while fixing provider-specific backend differences internally.

## Capabilities

### New Capabilities
- `codex-provider-support`: authenticate, expose models, and proxy Codex requests using the correct official-mode contract for both API-key and ChatGPT-backed flows.
- `multi-provider-routing`: resolve requests across Copilot and Codex, merge provider model catalogs, and surface provider-aware routing/status behavior.

### Modified Capabilities

None.

## Impact

- Affected code: `src/codex_auth.go`, `src/codex_provider.go`, `src/responses_adapter.go`, `src/router.go`, `src/models.go`, `src/config.go`, `src/main.go`, `src/cli.go`, and related tests.
- Affected behavior: `auth codex`, provider status/model listing, `/v1/chat/completions`, `/v1/embeddings`, and `/v1/models`.
- Affected operations: ChatGPT-backed Codex auth must honor official credential sources and treat `~/.codex/auth.json` as secret material.
