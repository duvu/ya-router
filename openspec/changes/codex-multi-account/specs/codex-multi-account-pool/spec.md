# Spec: codex-multi-account-pool

## Type: ADDED

## Requirements

### `Codex provider supports account pool configuration`

The `providers.codex` config section MUST support an `accounts` array. Each entry MUST have a `label` (non-empty string), an `Auth CodexAuthState`, and an optional `LastLimitedAt int64` (Unix timestamp).

A `account_cooldown_seconds` field (integer, default 300) MUST be supported on `CodexProviderConfig` to control the per-account cooldown window.

**Scenarios:**

- When `providers.codex.accounts` is present and non-empty, the service uses the pool directly.
- When `providers.codex.accounts` is absent or empty and `providers.codex.auth.mode` is non-empty, the legacy `auth` field MUST be transparently promoted to `accounts[0]` with label `"primary"` on config load. The top-level `auth` field is kept as-is for backward compat.
- When both `accounts` and the legacy `auth` field are absent, the service behaves as today (Codex disabled or prompt user to run `auth codex`).

### `Codex pool accounts are isolated`

Each account in the pool MUST have independent auth state. Authenticating or refreshing one account MUST NOT overwrite another account's credentials.

**Scenarios:**

- Running `auth codex --account secondary` authenticates the account labeled `"secondary"` without affecting the `"primary"` account's credentials.
- A rate-limit on one account MUST NOT prevent the other accounts from being used.
