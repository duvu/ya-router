## MODIFIED Requirements

### Requirement: Request routing is provider-prefix-aware
The router SHALL resolve prefixed model IDs to the corresponding provider as the first step in resolution, before checking `model_map` or performing catalog discovery. A prefixed ID that does not match any model from the indicated provider MUST return an error rather than falling back to a different provider.

#### Scenario: Prefixed model ID resolves deterministically
- **WHEN** a request specifies `gc-gpt-4o` and Copilot is enabled
- **THEN** the router routes to Copilot without consulting Codex, regardless of whether `gpt-4o` is also available from Codex

#### Scenario: Prefixed model ID with wrong provider returns error
- **WHEN** a request specifies `oc-claude-3.5-sonnet` (a Copilot-only model)
- **THEN** the router returns an error indicating the model is unavailable from the Codex provider

#### Scenario: Ambiguous bare model ID requires model_map or prefix
- **WHEN** a request specifies a bare model ID that is exposed by both Copilot and Codex
- **THEN** the router returns an error and the error message instructs the client to use a prefixed ID or add a `model_map` entry

#### Scenario: model_map entry resolves before prefix routing
- **WHEN** a request specifies a model ID that has a `model_map` entry
- **THEN** the `model_map` provider takes precedence over any prefix routing
