## ADDED Requirements

### Requirement: A comprehensive operator configuration guide exists
The project SHALL include a `docs/CONFIGURATION.md` file that explains all configurable fields, supported auth modes, provider-specific behaviour, model prefix convention, routing rules, and troubleshooting steps. The guide MUST be sufficient for an operator to configure and run the service without reading source code.

#### Scenario: Guide covers GitHub Copilot setup end-to-end
- **WHEN** an operator follows the GitHub Copilot section of `docs/CONFIGURATION.md`
- **THEN** they can authenticate with `auth copilot`, start the service, and send chat requests successfully

#### Scenario: Guide covers OpenAI Codex setup for both auth modes
- **WHEN** an operator follows the OpenAI Codex section of `docs/CONFIGURATION.md`
- **THEN** they can configure and authenticate with either `api_key` mode or `chatgpt`/`device_code` mode

#### Scenario: Guide documents the model prefix convention
- **WHEN** an operator reads the model prefix section of `docs/CONFIGURATION.md`
- **THEN** they understand that `gc-*` prefixes identify Copilot models and `oc-*` prefixes identify Codex models, and know how to use them in requests

#### Scenario: Guide documents model_map usage with examples
- **WHEN** an operator reads the routing section of `docs/CONFIGURATION.md`
- **THEN** they can configure `routing.model_map` entries for both prefixed and bare model IDs, with at least one worked example per provider

#### Scenario: Guide includes troubleshooting section
- **WHEN** an operator encounters a common error (e.g., unauthenticated, model not found, circuit breaker open)
- **THEN** the troubleshooting section in `docs/CONFIGURATION.md` provides a diagnosis and resolution path

### Requirement: docs/CONFIGURATION.md stays in sync with config.example.json
The configuration guide's schema reference section SHALL reflect the current fields in `config.example.json`. When `config.example.json` is updated, `docs/CONFIGURATION.md` MUST be updated in the same change.

#### Scenario: New config field is documented
- **WHEN** a new field is added to `config.example.json`
- **THEN** it appears with a description and example in `docs/CONFIGURATION.md`
