## ADDED Requirements

### Requirement: Requests route by model and capability across providers
The system SHALL resolve chat-completions and embeddings requests to the correct provider based on the requested model, configured routing rules, and provider capability support.

#### Scenario: Chat request routes to Codex by model
- **WHEN** a chat-completions request names a model owned by Codex
- **THEN** the router selects the Codex provider and preserves the resolved upstream model for that provider

#### Scenario: Embeddings request routes only to a supporting provider
- **WHEN** an embeddings request names a model
- **THEN** the router selects only a provider that exposes embeddings capability for that model or returns a clear error if none do

#### Scenario: Ambiguous model IDs require an explicit rule
- **WHEN** the same model ID is exposed by more than one enabled provider
- **THEN** the router returns an ambiguity error unless an explicit routing rule resolves it

### Requirement: Model listing merges provider catalogs safely
The system SHALL expose `/v1/models` as a merged view of enabled providers while keeping provider ownership internally traceable.

#### Scenario: Auth-ready providers contribute models
- **WHEN** `/v1/models` is requested and multiple providers are enabled
- **THEN** the response includes models from providers that are ready for use, subject to unavailable-model visibility policy

#### Scenario: Routing rules keep configured models visible
- **WHEN** a model exists in routing configuration but is not present in the live merged provider catalog
- **THEN** `/v1/models` still exposes that configured model entry for client routing continuity

### Requirement: Provider auth state remains isolated
The system MUST keep Copilot and Codex availability isolated so failure in one provider does not make the other unusable.

#### Scenario: One provider is unavailable
- **WHEN** one enabled provider cannot authenticate or list models
- **THEN** the remaining healthy provider continues serving its supported requests and model listings according to policy

#### Scenario: Status reports provider-specific readiness
- **WHEN** an operator requests provider status
- **THEN** the system reports readiness separately for Copilot and Codex rather than as one global authentication state
