## ADDED Requirements

### Requirement: Providers expose all available models without filtering
Both the Copilot and Codex providers SHALL return their complete model set from `ListModels` regardless of the `AllowedModels` config field.

#### Scenario: Copilot ListModels ignores AllowedModels
- **WHEN** `providers.copilot.allowed_models` is set to a non-empty list in config
- **THEN** `CopilotProvider.ListModels` returns all models from the upstream Copilot API without filtering

#### Scenario: Codex ListModels ignores AllowedModels
- **WHEN** `providers.codex.allowed_models` is set to a non-empty list in config
- **THEN** `CodexProvider.ListModels` returns the full Codex model set without filtering

#### Scenario: Empty AllowedModels continues to return all models
- **WHEN** `providers.copilot.allowed_models` or `providers.codex.allowed_models` is empty
- **THEN** all models are returned (existing behaviour; no regression)

#### Scenario: AllowedModels field remains in config without error
- **WHEN** a config file contains a non-empty `allowed_models` array
- **THEN** the service starts without error and the field is accepted but has no effect on model listing
