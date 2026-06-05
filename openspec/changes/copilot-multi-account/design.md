## Context

The service today has a single `providers.copilot.auth` slot (`CopilotAuthState`) containing one GitHub OAuth token and one derived Copilot bearer token. `EnsureAuthenticated` operates on that one slot. When the account hits its GitHub Copilot free-tier rate limit, all requests fail until the limit resets — there is no fallback.

The goal is to add a pool of accounts behind the existing `CopilotProvider` abstraction with zero breaking change to callers (proxy, router, models handler).

## Goals / Non-Goals

**Goals:**
- Operators can configure N Copilot accounts in config, each with a label and independent auth state.
- When the active account is rate-limited, the provider automatically advances to the next healthy account in the pool and retries the same request.
- `auth copilot --account <label>` authenticates a specific account.
- `status` shows per-account health when the pool has more than one entry.
- Single-account configs (existing `providers.copilot.auth` + no `accounts` array) continue to work without operator change — the legacy field is migrated transparently.

**Non-Goals:**
- Load-balancing or splitting traffic across multiple accounts simultaneously.
- Account rotation on non-rate-limit errors (model unavailable, upstream 5xx) — those are handled by the existing model-rotation logic.
- Adding or removing accounts at runtime without restart.

## Key Decisions

1. **Config schema**: Add `CopilotAccount` struct (label, auth `CopilotAuthState`, `last_limited_at int64`). `CopilotProviderConfig.Accounts []CopilotAccount` is the pool. The top-level `Auth` field is kept for backward compat; on load, if `Accounts` is empty and `Auth` is non-zero, it is promoted to `Accounts[0]` with label `"primary"`. This keeps the existing single-account config valid without any operator migration step.

2. **Account cursor**: `CopilotProvider` adds a mutex-protected `accountCursor int` (pool index of the currently active account). `authState()` returns the active account's `CopilotAuthState`.

3. **Failover trigger**: Account-level limit detection reuses the existing `shouldShiftToNextCopilotModel` response classifier, but at a higher level. If _all_ model attempts for the active account return limit/auth signals, the provider calls `advanceAccount()` which records `last_limited_at` on the exhausted account, advances the cursor mod pool size, skips accounts limited within the last `account_cooldown_seconds` (default 300 s), and retries the request with the new account. If all accounts are in cooldown, the last upstream response is forwarded as-is.

4. **EnsureAuthenticated scope**: `EnsureAuthenticated` now authenticates the active account only. The pool does not eagerly pre-authenticate all accounts — accounts authenticate on demand when they become active.

5. **Save discipline**: `save()` serializes the full config including all accounts' updated auth states. This is unchanged from today for the primary account.

6. **CLI auth**: `handleAuthCopilot` gains an optional `--account <label>` flag. If the label matches an existing pool entry, it authenticates that entry in place. If the label is new, it appends a new entry to the pool. No flag = authenticates the first (index 0) account as before.

## Risks / Trade-offs

- **Token proliferation**: Multiple GitHub OAuth tokens in one config file is a larger blast radius if the config leaks. Mitigated by existing 0600 permissions.
- **Stale `last_limited_at`**: If the service restarts after a short limit window, accounts are allowed to retry immediately. Acceptable since the limit window is server-side.
- **Cursor not persisted**: Account cursor resets to 0 on restart, so the service always starts from the first account. This is intentional — it keeps state simple.

## Migration Plan

No data migration required. Config load logic handles backward compat: if `providers.copilot.auth.github_token` is non-empty and `providers.copilot.accounts` is absent or empty, the auth field is used directly as the sole pool entry. Operators can add more accounts by appending to `accounts[]` and running `auth copilot --account <label>` for each.
