## 1. Config Schema

- [x] 1.1 In `src/config.go`: Add `CodexAccount` struct with fields `Label string`, `Auth CodexAuthState`, `LastLimitedAt int64 json:"last_limited_at,omitempty"`
- [x] 1.2 In `src/config.go`: Add `Accounts []CodexAccount json:"accounts,omitempty"` to `CodexProviderConfig`; keep existing `Auth CodexAuthState` field with `json:"auth"` for backward compat
- [x] 1.3 In `src/config.go`: Add `normalizeCodexAccounts` helper: if `Accounts` is empty and `Auth.Mode` is non-empty, promote `Auth` to `Accounts[0]` with label `"primary"` and zero out the top-level `Auth` field; call this from `applyConfigDefaults` and `migrateV0ToV1`
- [x] 1.4 Add `AccountCooldownSeconds int json:"account_cooldown_seconds,omitempty"` to `CodexProviderConfig` (default 300 in `applyConfigDefaults`)

## 2. Provider Account Pool

- [x] 2.1 In `src/codex_provider.go` `CodexProvider`: add `accountCursor int` field (protected by existing `mu`)
- [x] 2.2 Add `activeAccount() *CodexAccount`: returns pointer to `cfg.Providers.Codex.Accounts[accountCursor]` or nil when pool empty
- [x] 2.3 Replace `authState()` to return `&activeAccount().Auth` when pool is non-empty, falling back to `&p.cfg.Providers.Codex.Auth` when pool is empty
- [x] 2.4 Add `cooldownSeconds() int64` helper returning `AccountCooldownSeconds` (or 300 default)
- [x] 2.5 Add `isCodexAccountInCooldown(acc *CodexAccount) bool` helper
- [x] 2.6 Add `firstHealthyCodexAccount() int`: returns first non-cooldown index, or 0 if all in cooldown
- [x] 2.7 Add `advanceCodexAccount() bool`: stamps `LastLimitedAt` on current account, finds next healthy; returns false if all exhausted or only 1 account
- [x] 2.8 Update `NewCodexProvider` to initialize cursor via `firstHealthyCodexAccount()`

## 3. Request Failover Logic

- [x] 3.1 In `src/codex_provider.go` `ProxyRequest`: wrap the existing request/response logic in an outer account-failover loop (`maxAccountAttempts = max(1, len(accounts))`)
- [x] 3.2 After upstream response received: call `isAccountLimitSignal(resp)` (reuse the package-level function from `copilot_provider.go`); if true, call `advanceCodexAccount()` and retry with new account credentials; otherwise continue as before
- [x] 3.3 If all accounts exhausted (loop completes), forward the last rate-limit response as-is

## 4. CLI: auth command

- [x] 4.1 In `src/cli.go` `handleAuthCodex`: accept `accountLabel string` parameter; call `resolveOrCreateCodexAccount(cfg, label)` to get the auth state pointer to authenticate into
- [x] 4.2 Add `resolveOrCreateCodexAccount(cfg *Config, label string) *CodexAuthState`: finds pool entry by label, appends new entry if not found, uses `Accounts[0]` when label empty; returns pointer to the matched/created entry's `Auth`
- [x] 4.3 Wire `--account` flag in `src/main.go` `auth` command parsing; pass it to `handleAuthCodex`
- [x] 4.4 `handleAuthCodexAPIKey` and `handleAuthCodexManualToken`: also use `resolveOrCreateCodexAccount` to store into the correct pool entry
- [x] 4.5 `refreshCodex`: iterate all pool accounts with non-empty auth state; fall back to legacy `Auth` when pool is empty

## 5. CLI: status command

- [x] 5.1 In `src/cli.go` `printCodexStatus`: if pool has 2+ accounts, print per-account table (label, mode, authenticated bool, expiry if known, cooldown status); if 1 account or legacy, keep existing display

## 6. Tests

- [x] 6.1 Unit test for `normalizeCodexAccounts`: legacy `auth` with mode non-empty is promoted to `accounts[0]` with label `"primary"`
- [x] 6.2 Unit test for `advanceCodexAccount`: cursor advances past in-cooldown accounts; returns false when all are in cooldown
- [x] 6.3 Unit test for `isCodexAccountInCooldown`: recently limited account is in cooldown; expired account is eligible
- [x] 6.4 Integration test for account failover: pool of 2 accounts where account 0 always returns 429; assert cursor advances to account 1
- [x] 6.5 Integration test: all accounts in cooldown/exhausted returns last 429 without infinite loop

## 7. Config Example and Documentation

- [x] 7.1 Update `config.example.json`: add `providers.codex.accounts` array with two example entries (one chatgpt, one api_key); keep top-level `auth` as zero/empty
- [x] 7.2 Update `docs/CONFIGURATION.md`: add section on multi-account Codex configuration, `--account` CLI flag, cooldown behavior, and backward compat note; explain credential storage (config pool, not just official store)

## 8. Verification

- [x] 8.1 Run `make fmt && make vet && make test && make build`; confirm all pass
