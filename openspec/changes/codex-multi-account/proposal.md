## Why

The OpenAI Codex ChatGPT Plus free tier enforces per-account rate limits. With a single-account setup, the proxy becomes unavailable whenever that account is exhausted. Supporting a pool of Codex accounts lets the service rotate to the next healthy account automatically, giving continuous availability without manual intervention — mirroring the multi-account pool just added for GitHub Copilot.

## What Changes

**New capabilities:**

- `codex-multi-account-pool`: Operators can configure multiple Codex accounts under `providers.codex.accounts[]`. Each account has its own label and auth state (`CodexAccount{Label, Auth CodexAuthState, LastLimitedAt int64}`). The existing single `providers.codex.auth` field becomes a legacy alias for the primary account (backward compat: promoted to `accounts[0]` with label `"primary"` on first load when `accounts` is absent).
- `codex-account-failover`: When a request returns a rate-limit signal (HTTP 429, or HTTP 403 with "rate limit"/"exceeded"/"quota" in the body) from the active account, the provider advances the cursor to the next non-cooldown account and retries. If all accounts are exhausted or in cooldown, the last upstream response is forwarded. A configurable `account_cooldown_seconds` (default 300) prevents rapid re-selection of recently limited accounts.

**Modified capabilities:**

- `codex-provider-support` (delta): `authState()` now reads from the active pool account instead of the single `Auth` field. `EnsureAuthenticated` authenticates the active account only; per-account credentials are stored in `Accounts[cursor].Auth` in config. The official Codex store (`~/.codex/auth.json`) remains the source for the primary/active account when performing device-code auth.
- `auth codex` command: gains an optional `--account <label>` flag to authenticate a named account. Without the flag, authenticates the primary (first) account as before.
- `status` command: shows per-account authentication state and last-limit timestamp when multiple accounts are configured.

**Breaking changes:** None. Single-account configs continue to work as-is.

## Capabilities

### New Capabilities

- `codex-multi-account-pool`: Config schema extension adding `CodexAccount` struct, `accounts []CodexAccount` field, `account_cooldown_seconds` to `CodexProviderConfig`, and normalization of the legacy `auth` field into the pool on load.
- `codex-account-failover`: Automatic account rotation in `CodexProvider.ProxyRequest` on rate-limit signals, guarded by a per-account cooldown window.

### Modified Capabilities

- `codex-provider-support`: `authState()` and `EnsureAuthenticated` updated to operate on the active pool account instead of the single `Auth` field. Per-account credentials stored in config pool; official store interaction scoped to the current active account.

## Impact

- `src/config.go`: Add `CodexAccount` struct and `Accounts []CodexAccount` + `AccountCooldownSeconds int` to `CodexProviderConfig`; add `normalizeCodxAccounts` helper called from `applyConfigDefaults` and `migrateV0ToV1`.
- `src/codex_provider.go`: Add `accountCursor int`, `activeAccount()`, `authState()` pool version, `advanceAccount()`, `isCodexAccountInCooldown()`, failover loop in `ProxyRequest`.
- `src/cli.go`: `handleAuthCodex` gains `--account` flag; `printCodexStatus` shows per-account table when pool has 2+ entries; `refreshCodex` iterates pool accounts.
- `src/codex_auth.go`: `persistToOfficialStore` / `loadOfficialCodexAuth` continue to interact with `~/.codex/auth.json` only for the active/primary account during device-code auth; per-account runtime credentials live in config pool.
- Tests: new unit tests for pool normalization, cursor advancement, cooldown, and proxy failover integration tests.
- `config.example.json`, `docs/CONFIGURATION.md`: updated to document multi-account Codex setup.
