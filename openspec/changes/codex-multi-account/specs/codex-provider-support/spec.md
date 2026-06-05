# Spec: codex-provider-support (delta)

## Type: MODIFIED

## Delta Requirements

### `CodexProvider uses pool-based auth state`

`CodexProvider.authState()` MUST return the auth state of the currently-active pool account (identified by `accountCursor`), falling back to the legacy `providers.codex.auth` field when the pool is empty.

`EnsureAuthenticated` MUST only authenticate the active account. It MUST NOT pre-authenticate all pool accounts.

**Scenarios:**

- When the pool is non-empty, `authState()` returns `&Accounts[accountCursor].Auth`.
- When the pool is empty, `authState()` returns `&cfg.Providers.Codex.Auth` (legacy fallback).

### `Per-account credentials are stored in config pool, not only in official store`

After a successful `auth codex --account <label>` device-code flow, the resulting credentials (AccessToken, RefreshToken, ExpiresAt, AccountID) MUST be stored in the matching `Accounts[i].Auth` entry in config (in addition to `~/.codex/auth.json` for the currently-authenticated account).

Runtime `EnsureAuthenticated` MUST resolve credentials from `Accounts[cursor].Auth` directly (populated during auth) rather than always re-reading `~/.codex/auth.json` at request time.

**Scenarios:**

- After `auth codex --account primary`, `Accounts[0].Auth.AccessToken` is non-empty and `Accounts[0].Auth.ExpiresAt > 0`.
- `EnsureAuthenticated` for the active account resolves from `Accounts[cursor].Auth` first; only falls back to the official store if `Auth.AccessToken` is empty.
