## Why

The GitHub Copilot free tier enforces per-account rate limits. A single-account setup means the proxy becomes unavailable when that account is exhausted. Supporting a pool of Copilot accounts lets the service rotate to the next healthy account automatically, giving continuous availability without manual intervention.

## What Changes

**New capabilities:**

- `copilot-multi-account-pool`: Operators can configure multiple Copilot accounts (GitHub tokens) under `providers.copilot.accounts[]`. Each account has its own auth state (label, GitHub token, Copilot token, expiry). The existing single `providers.copilot.auth` field becomes a legacy alias for the primary account (backward compat).
- `copilot-account-failover`: When a request to the active account returns a rate-limit (HTTP 429) or account-suspension signal (HTTP 403 with a "rate limit" or "exceeded" body) on all model attempts, the provider automatically advances to the next available account in the pool and retries the request. Account selection is round-robin across healthy accounts.

**Modified capabilities:**

- `auth copilot` command: gains an optional `--account <label>` flag to authenticate a named account. Without the flag, authenticates the primary (first) account as before.
- `status` command: shows per-account authentication state and last-limit timestamp when multiple accounts are configured.
- Config schema: `CopilotProviderConfig` gains an `accounts []CopilotAccount` array. Each entry holds a `label` (string, required), `auth CopilotAuthState`, and optional `last_limited_at int64` (Unix timestamp the account was last rate-limited; used for cooldown ordering). The legacy `auth` top-level field is still loaded and mapped to the primary account automatically.

**Breaking changes:** None. Single-account configs continue to work as-is.

## Impact

- Affects `src/config.go` (schema), `src/copilot_provider.go` (account pool, failover loop), `src/auth.go` (no changes needed — operates on `CopilotAuthState`), `src/cli.go` (auth and status commands), tests.
- Config migration: if `providers.copilot.auth` is non-empty and `providers.copilot.accounts` is empty, migrate auth to `accounts[0]` with label `"primary"` on first load.
