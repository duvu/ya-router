## ADDED Requirements

### Requirement: Rust service uses async tokio runtime
The Rust binary SHALL use tokio as its async executor for all I/O including HTTP serving, HTTP client requests, and file access.

#### Scenario: Server starts and accepts connections concurrently
- **WHEN** the Rust binary is started with the `run` command
- **THEN** it SHALL bind to the configured port and handle multiple simultaneous HTTP connections without blocking

### Requirement: HTTP server implemented with axum
The Rust service SHALL use axum for HTTP routing with handlers for `/health`, `/v1/models`, `/v1/chat/completions`, `/v1/embeddings`.

#### Scenario: Health endpoint returns correct shape
- **WHEN** a GET request is made to `/health`
- **THEN** the server SHALL return HTTP 200 with JSON `{"status":"ok","service":"github-copilot-svcs"}`

#### Scenario: Unknown routes return 404
- **WHEN** a request is made to any path not in the registered route set
- **THEN** the server SHALL return HTTP 404 with a JSON error body

### Requirement: SSE proxy uses streaming reqwest response body
For streaming chat completions, the Rust service SHALL pipe the upstream SSE response body directly to the client without buffering the full response.

#### Scenario: Streaming chat completions are proxied chunk-by-chunk
- **WHEN** a POST to `/v1/chat/completions` has `"stream": true` in the request body
- **THEN** the server SHALL forward each SSE chunk to the client as it arrives from upstream, with `Content-Type: text/event-stream`

#### Scenario: Non-streaming responses are buffered and returned
- **WHEN** a POST to `/v1/chat/completions` does not include `"stream": true`
- **THEN** the server SHALL buffer the upstream response and return it as a single HTTP 200 response
