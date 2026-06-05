## ADDED Requirements

### Requirement: Provider trait defines async interface
The Rust service SHALL define a `Provider` trait with async methods: `ensure_authenticated`, `chat_completions`, `embeddings`, `list_models`.

#### Scenario: Router holds provider instances via trait object
- **WHEN** the service starts and providers are initialized from config
- **THEN** the router SHALL hold `Arc<dyn Provider + Send + Sync>` instances, not concrete types

### Requirement: Copilot provider implements full auth and runtime
The Rust Copilot provider SHALL implement GitHub device-flow authentication, token refresh, free-model-rotation for chat requests, and embeddings proxying.

#### Scenario: Copilot chat uses free-model rotation
- **WHEN** a chat completions request is routed to Copilot
- **THEN** the provider SHALL ignore the requested model field and select a model from the configured allowed models pool

#### Scenario: Copilot token refresh on expiry
- **WHEN** the Copilot access token is expired or missing
- **THEN** `ensure_authenticated` SHALL attempt to refresh using the stored refresh token before returning an error

### Requirement: Codex provider implements both api_key and chatgpt auth modes
The Rust Codex provider SHALL implement credential resolution for `api_key` mode (env → config → official store) and `chatgpt` mode (official store → config fallback), with transport selected by mode.

#### Scenario: Codex api_key mode prefers OPENAI_API_KEY env
- **WHEN** `OPENAI_API_KEY` is set in the environment and Codex is in api_key mode
- **THEN** `ensure_authenticated` SHALL use the env var as the API key, not the config value

#### Scenario: Codex chatgpt mode prefers official store
- **WHEN** Codex is in chatgpt mode and `~/.codex/auth.json` (or `$CODEX_HOME/auth.json`) contains valid tokens
- **THEN** `ensure_authenticated` SHALL use the official store tokens, not config tokens

#### Scenario: Codex auth logs no raw secret values
- **WHEN** any Codex auth operation logs provider state
- **THEN** logs SHALL NOT contain raw access tokens, refresh tokens, API keys, or account IDs; only boolean/metadata values are permitted
