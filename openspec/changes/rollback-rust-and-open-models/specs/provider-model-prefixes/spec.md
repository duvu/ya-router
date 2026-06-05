## MODIFIED Requirements

### Requirement: Provider prefix applied to full model set
The `modelsHandler` SHALL apply provider prefixes (`gc-` for Copilot, `oc-` for Codex) to the complete model set returned by each provider, not to a filtered subset. This requirement replaces the earlier understanding that prefix injection operated on an `allowed_models`-filtered list.

#### Scenario: All Copilot upstream models appear with gc- prefix
- **WHEN** Copilot is authenticated and returns N models from its upstream API
- **THEN** `/v1/models` includes all N models prefixed with `gc-`, with no model suppressed by `AllowedModels`

#### Scenario: All Codex models appear with oc- prefix
- **WHEN** Codex is enabled and authenticated
- **THEN** `/v1/models` includes all Codex models prefixed with `oc-`, regardless of any `allowed_models` config value
