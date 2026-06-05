## ADDED Requirements

### Requirement: Rust runtime preserves external service contract
The system SHALL provide a Rust implementation that preserves the current external behavior of the service, including the OpenAI-compatible HTTP endpoints, the operator-facing CLI commands, and the existing runtime/auth path expectations.

#### Scenario: HTTP endpoint parity
- **WHEN** the Rust runtime is started
- **THEN** it exposes `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, and `/health` with behavior compatible with the current service contract

#### Scenario: CLI parity
- **WHEN** an operator invokes `run`, `auth`, `status`, `config`, `models`, `refresh`, `migrate-config`, or `version` against the Rust runtime
- **THEN** the command surface and operational intent remain available without requiring a different workflow model

#### Scenario: Runtime path parity
- **WHEN** the Rust runtime reads local configuration or Codex auth state
- **THEN** it honors `~/.local/share/github-copilot-svcs/config.json` and the configured or default Codex auth store path with compatible semantics

### Requirement: Rust runtime preserves provider and routing semantics
The system SHALL preserve the existing provider-isolation, model-routing, auth-mode, and model-list exposure behavior when implementing the Rust runtime.

#### Scenario: Multi-provider routing parity
- **WHEN** a request targets a configured model/provider combination
- **THEN** the Rust runtime resolves providers and capability constraints using behavior compatible with the current service rules

#### Scenario: Copilot chat special handling parity
- **WHEN** a Copilot chat request is proxied through the Rust runtime
- **THEN** the runtime preserves the current special handling that ignores the client model and applies the service’s Copilot model-selection behavior

#### Scenario: Codex auth-mode parity
- **WHEN** Codex runs in API-key mode or ChatGPT/device-auth mode under the Rust runtime
- **THEN** the runtime preserves the current credential-source and auth-isolation behavior for each mode

### Requirement: Rust runtime uses explicit subsystem boundaries
The system SHALL implement the Rust port with explicit boundaries for entrypoint/CLI, config migration, providers, routing/model catalog, transforms, and HTTP proxy transport.

#### Scenario: Migration-ready module structure
- **WHEN** implementation begins for the Rust runtime
- **THEN** the source layout separates the major runtime responsibilities into distinct modules or crates instead of recreating one flat file group
