# Spec: codex-account-failover

## Type: ADDED

## Requirements

### `Codex provider rotates accounts on rate-limit signals`

When a `ProxyRequest` call receives a rate-limit signal from the active account, the provider MUST advance to the next non-cooldown account in the pool and retry the request.

Rate-limit signals are: HTTP 429 on any request, OR HTTP 403 with a response body containing "rate limit", "exceeded", or "quota" (case-insensitive).

**Scenarios:**

- When account 0 returns HTTP 429 and account 1 is healthy: request is retried using account 1's credentials and account 1's response is returned to the client.
- When all accounts are in cooldown or exhausted: the last upstream response (e.g., 429) is forwarded to the client as-is without infinite looping.
- The failover loop MUST run at most `max(1, len(accounts))` times to prevent infinite loops.

### `Rate-limited accounts enter a cooldown window`

When `advanceAccount()` is called, the exhausted account's `LastLimitedAt` MUST be set to the current Unix timestamp. Subsequent account selection MUST skip accounts whose `LastLimitedAt` is within the last `account_cooldown_seconds` seconds.

**Scenarios:**

- An account with `LastLimitedAt = time.Now().Unix()` (just limited) MUST be skipped during account selection.
- An account with `LastLimitedAt = time.Now().Unix() - (cooldownSeconds + 1)` (expired cooldown) MUST be eligible for selection.
- When all accounts are in cooldown, the first account MUST be selected (no infinite skip loop).
