## ADDED Requirements

### Requirement: Docker auth writes use a writable config directory
The system SHALL ensure the mounted config directory used for auth persistence is writable by the runtime user before the service process starts.

#### Scenario: Container startup repairs mount ownership
- **WHEN** the service starts inside Docker with a bind-mounted config directory
- **THEN** the entrypoint SHALL fix ownership and permissions on that directory so the runtime user can create `config.json.tmp`

#### Scenario: Auth commands can persist configuration
- **WHEN** an operator runs `auth copilot` or `auth codex` inside the container
- **THEN** the service SHALL be able to write the updated config file without a `permission denied` error
