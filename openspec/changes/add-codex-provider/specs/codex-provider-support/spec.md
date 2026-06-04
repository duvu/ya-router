## ADDED Requirements

### Requirement: Codex auth mode determines the credential source
The system SHALL support Codex authentication in at least `api_key` mode and ChatGPT-backed device-auth mode, and each mode MUST use its own credential source and validation path.

#### Scenario: API-key mode uses Platform credentials
- **WHEN** Codex is configured in `api_key` mode
- **THEN** the service uses the configured API key source for Codex requests and does not require ChatGPT-backed credential metadata

#### Scenario: ChatGPT-backed mode uses official Codex credentials
- **WHEN** Codex is configured in ChatGPT-backed or device-auth mode
- **THEN** the service loads credentials from the official Codex credential source and any required account metadata instead of treating the credential as a generic Platform API token

### Requirement: Codex transport is selected by auth mode
The system SHALL use distinct upstream transport behavior for Platform API-key mode and ChatGPT-backed Codex mode.

#### Scenario: Platform mode targets Platform-compatible upstream behavior
- **WHEN** a Codex request is routed in `api_key` mode
- **THEN** the provider sends the request using Platform-compatible auth and request shaping

#### Scenario: ChatGPT-backed mode targets ChatGPT Codex behavior
- **WHEN** a Codex request is routed in ChatGPT-backed or device-auth mode
- **THEN** the provider sends the request using the ChatGPT Codex transport contract and required upstream metadata for that mode

### Requirement: Codex secret material remains private
The system MUST treat Codex local auth stores, bearer tokens, refresh tokens, and API keys as secret material.

#### Scenario: Secret data is absent from operator-visible surfaces
- **WHEN** operators use CLI auth, status, logs, or API endpoints
- **THEN** the system does not echo or expose Codex secret contents

#### Scenario: Missing or unusable Codex credentials fail safely
- **WHEN** Codex credentials are missing, expired, or incompatible with the requested mode
- **THEN** the system returns a clear provider-specific error without exposing secret data
