## ADDED Requirements

### Requirement: Account-level failover on rate limit

When a Copilot request is rate-limited (HTTP 429) or rejected with an account-level limit signal (HTTP 403 with body indicating rate limit or usage exceeded) on all model attempts for the active account, the service MUST advance to the next healthy account and retry the request.

**Scenario: Active account exhausted, next account is healthy**
- Given the pool has two accounts: account A (active) and account B
- And account A returns HTTP 429 for all model attempts
- When `ProxyFreeChatRequest` is called
- Then the provider records `last_limited_at` on account A
- And advances the account cursor to account B
- And retries the request using account B's credentials
- And returns account B's response to the caller

**Scenario: All accounts in cooldown**
- Given all pool accounts have `last_limited_at` within the last `account_cooldown_seconds`
- When `ProxyFreeChatRequest` is called
- Then the provider does NOT retry across accounts
- And forwards the last upstream 429/403 response to the caller with no modification

**Scenario: Cooldown window respected**
- Given account A has `last_limited_at` set to more than `account_cooldown_seconds` ago
- When account selection is evaluated
- Then account A is treated as healthy and eligible for selection

### Requirement: Account-level failover is transparent to callers

**Scenario: Caller receives a single response**
- Given account failover occurs mid-request
- When the retry on the next account succeeds
- Then the caller receives a single normal HTTP response (no double response, no error mid-stream)
- And the fact that failover occurred is logged but not surfaced to the caller

### Requirement: Non-rate-limit errors do NOT trigger account failover

**Scenario: Transport error does not advance account**
- Given a network error on the upstream connection
- When `ProxyFreeChatRequest` handles the error
- Then the provider does NOT advance the account cursor (the model-rotation logic applies as before)
- And the error is returned to the caller as before

**Scenario: 5xx server error does not advance account**
- Given the upstream returns HTTP 503
- When `shouldShiftToNextCopilotModel` is evaluated
- Then the provider shifts to the next model (existing behavior)
- But does NOT advance the account cursor
