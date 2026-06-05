## Context

The service has a single `providers.codex.auth` slot (`CodexAuthState`) holding one account's credentials (mode, API key or access/refresh tokens, account ID). `EnsureAuthenticated` and `ProxyRequest` operate on that single slot. When a ChatGPT Plus account exhausts its rate limit, all Codex requests fail until the limit resets — there is no fallback.

The `CodexAuthState` struct already carries all necessary per-account fields (`Mode`, `APIKey`, `AccessToken`, `RefreshToken`, `ExpiresAt`, `AccountID`), so the per-account container for the pool requires no new fields — just a wrapper with a label and cooldown timestamp.

This change mirrors the Copilot multi-account pool (`copilot-multi-account`) using the same patterns: pool config schema, cursor-based account selection, cooldown window, failover loop in `ProxyRequest`, and CLI `--account` flag.

## Goals / Non-Goals

**Goals:**
- Operators can configure N Codex accounts in config, each with a label and independent `CodexAuthState`.
- When the active account returns a rate-limit signal, `ProxyRequest` automatically advances to the next healthy account in the pool and retries.
- `auth codex --account <label>` (or `--account <label> --api-key`) authenticates a specific named account. The official Codex store (`~/.codex/auth.json`) is used for device-code auth of the currently-being-authenticated account.
- `status` shows per-account health when the pool has 2+ entries.
- Single-account configs (existing `providers.codex.auth`) continue to work without operator change.

**Non-Goals:**
- Load-balancing or splitting traffic across accounts simultaneously.
- Account rotation on non-rate-limit errors (5xx upstream errors are handled by the circuit breaker).
- Persisting multi-account credentials to the official Codex store (that file supports one account; per-account runtime credentials live in config).

## Decisions

1. **Config schema**: Add `CodexAccount{Label string, Auth CodexAuthState, LastLimitedAt int64}`. `CodexProviderConfig.Accounts []CodexAccount` is the pool. Add `AccountCooldownSeconds int` (default 300). The legacy `Auth` field is kept for backward compat; `normalizeCodexAccounts()` (called from `applyConfigDefaults` and `migrateV0ToV1`) promotes `Auth` to `Accounts[0]` with label `"primary"` when `Accounts` is empty and `Auth.Mode` is non-empty.

2. **Account cursor**: `CodexProvider` adds a mutex-protected `accountCursor int`. `activeAccount()` returns `*CodexAccount` for the current cursor. `authState()` returns `&activeAccount().Auth` or falls back to the legacy `Auth` field when the pool is empty.

3. **Official store scoping**: `loadOfficialCodexAuth` and `persistToOfficialStore` are called only during `handleAuthCodex` (device-code auth flow). They write/read `~/.codex/auth.json` for the account currently being authenticated. After auth, the credentials are stored in `Accounts[cursor].Auth` in config; subsequent `EnsureAuthenticated` calls resolve from config directly, not from the official store.

4. **Failover trigger**: Same signal as Copilot — HTTP 429, or HTTP 403 with "rate limit"/"exceeded"/"quota" body. Detected in `ProxyRequest` after the upstream response is received. If a limit signal is received, `advanceAccount()` is called; if a healthy account is found, the whole request is retried with that account's credentials. Guard: loop runs at most `max(1, len(accounts))` times.

5. **`EnsureAuthenticated` scope**: Only authenticates the active account. No eager pre-authentication of all accounts; accounts authenticate on demand when they become active.

6. **CLI auth**: `handleAuthCodex` already takes `mode` and `token` parameters. It gains an `accountLabel` parameter. The account resolution logic is the same as Copilot: find by label, or append new if not found.

7. **`refreshCodex`**: Iterates all pool accounts with non-empty auth state (matching existing Copilot pattern).

## Risks / Trade-offs

- **Multiple credentials in config**: Same concern as Copilot — config is 0600, acceptable blast radius.
- **One official store, N accounts**: Only one account can be the "source of truth" for `~/.codex/auth.json` at a time. This is intentional — the official store is the auth handshake channel, not the runtime credential store. After handshake, credentials move to config's `Accounts[i].Auth`.
- **Cursor not persisted**: Resets to 0 on restart, same as Copilot pool. Intentional.
