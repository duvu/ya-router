## ADDED Requirements

### Requirement: Copilot account pool configuration

The service MUST support a `providers.copilot.accounts` array in the config file. Each entry in the array MUST contain:
- `label` (string, required): unique identifier for the account (e.g. `"primary"`, `"account2"`)
- `auth` (`CopilotAuthState`): independent auth state for this account (github_token, copilot_token, expires_at, refresh_in)
- `last_limited_at` (int64, optional): Unix timestamp when this account was last rate-limited

**Scenario: Operator configures two accounts**
- Given `providers.copilot.accounts` has two entries with distinct labels and distinct GitHub tokens
- When the service starts
- Then both accounts are available in the pool and the first account is the active account

**Scenario: Single-account legacy config is compatible**
- Given `providers.copilot.auth` is non-empty and `providers.copilot.accounts` is absent or empty
- When the service loads config
- Then the service treats `providers.copilot.auth` as the sole pool entry (label `"primary"`)
- And the operator does NOT need to change their config file

### Requirement: Per-account authentication via CLI

The `auth copilot` command MUST support an optional `--account <label>` flag.

**Scenario: Authenticate a named account**
- Given the operator runs `auth copilot --account account2`
- When the GitHub device-flow completes
- Then the resulting tokens are stored under `providers.copilot.accounts` entry with label `"account2"`
- And other accounts' tokens are not modified

**Scenario: Authenticate without flag uses first account**
- Given the operator runs `auth copilot` (no `--account` flag)
- When the device-flow completes
- Then the tokens are stored in the first (index 0) account entry as before

### Requirement: Per-account status display

**Scenario: Status shows pool summary**
- Given `providers.copilot.accounts` has two or more entries
- When the operator runs `status`
- Then each account's label, authentication state (authenticated / needs auth), token expiry, and last_limited_at (if non-zero) are printed
