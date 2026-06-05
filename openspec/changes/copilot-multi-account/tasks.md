## 1. Config Schema

- [x] 1.1 In `src/config.go`: Add `CopilotAccount` struct with fields `Label string`, `Auth CopilotAuthState`, `LastLimitedAt int64 json:"last_limited_at,omitempty"`
- [x] 1.2 In `src/config.go`: Add `Accounts []CopilotAccount json:"accounts,omitempty"` to `CopilotProviderConfig`; keep existing `Auth CopilotAuthState` field with `json:"auth"` for backward compat
- [x] 1.3 In `src/config.go` `loadConfig` (or a new `normalizeCopilotAccounts` helper): if `Accounts` is empty and `Auth.GitHubToken` is non-empty, promote `Auth` to `Accounts[0]` with label `"primary"` and zero out the top-level `Auth` field
- [x] 1.4 Add `AccountCooldownSeconds int` to `CopilotProviderConfig` (default 300) for the per-account cooldown window

## 2. Provider Account Pool

- [x] 2.1 In `src/copilot_provider.go` `CopilotProvider`: replace single `cfg.Providers.Copilot.Auth` usage with account-pool cursor: add `accountCursor int` field (protected by existing `mu`)
- [x] 2.2 Replace `authState()` helper to return a pointer to `cfg.Providers.Copilot.Accounts[accountCursor].Auth` (with bounds check)
- [x] 2.3 Add `activeAccount()` helper that returns `*CopilotAccount` for the current cursor position
- [x] 2.4 Add `advanceAccount()` method: records `LastLimitedAt = time.Now().Unix()` on current account, advances cursor to next non-cooldown account; returns `true` if a healthy account was found, `false` if all are in cooldown
- [x] 2.5 Add `isAccountInCooldown(acc *CopilotAccount) bool` helper checking `acc.LastLimitedAt > 0 && time.Now().Unix()-acc.LastLimitedAt < int64(cooldownSeconds)`
- [x] 2.6 Update `NewCopilotProvider` to initialize cursor to first healthy (non-cooldown) account

## 3. Request Failover Logic

- [x] 3.1 Add `isAccountLimitSignal(resp *http.Response) bool` helper: returns true for HTTP 429 on any attempt, or HTTP 403 with body containing "rate limit", "exceeded", "quota", "limit" (case-insensitive, peek first 512 bytes)
- [x] 3.2 In `ProxyFreeChatRequest`: after all model attempts for the active account are exhausted AND the last response was an account-limit signal, call `advanceAccount()` — if a new healthy account was found, retry the full model-attempt loop with the new account's credentials; otherwise forward the last response
- [x] 3.3 Add a `maxAccountFailovers` guard (equal to pool size) to prevent infinite loops if all accounts return limit signals simultaneously

## 4. CLI: auth command

- [x] 4.1 In `src/cli.go` `handleAuthCopilot`: add `--account <label>` flag parsing (via `flag.FlagSet`)
- [x] 4.2 If `--account` is provided: find existing pool entry by label (create new entry if not found), authenticate that entry's auth state, save config
- [x] 4.3 If no `--account`: authenticate `Accounts[0]` (primary) as before; if pool is empty, append a new primary account

## 5. CLI: status command

- [x] 5.1 In `src/cli.go` `handleStatus` / `printCopilotStatus`: if pool has 2+ accounts, print per-account table (label, authenticated, expiry, last_limited_at if non-zero); if 1 account, keep existing single-account display

## 6. Tests

- [x] 6.1 Unit test for `normalizeCopilotAccounts`: legacy `auth` field is promoted to `accounts[0]` with label `"primary"`
- [x] 6.2 Unit test for `advanceAccount`: cursor advances past in-cooldown accounts, returns false when all are in cooldown
- [x] 6.3 Unit test for `isAccountLimitSignal`: HTTP 429 returns true; HTTP 403 with "rate limit" body returns true; HTTP 403 with unrelated body returns false; HTTP 503 returns false
- [x] 6.4 Integration test for account failover: mock pool of two accounts where account 0 always returns 429; assert response comes from account 1
- [x] 6.5 Integration test: all accounts in cooldown returns last upstream 429 response without infinite loop

## 7. Config Example and Documentation

- [x] 7.1 Update `config.example.json`: add `providers.copilot.accounts` array with two example entries; keep top-level `auth` as empty/zero to show the new schema shape
- [x] 7.2 Update `docs/CONFIGURATION.md`: add section on multi-account Copilot configuration, `--account` CLI flag, cooldown behavior, and backward compat note for single-account configs

## 8. Verification

- [x] 8.1 Run `make fmt && make vet && make test && make build`; confirm all pass
