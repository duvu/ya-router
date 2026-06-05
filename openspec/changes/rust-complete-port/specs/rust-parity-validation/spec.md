## ADDED Requirements

### Requirement: Parity test harness compares Go and Rust side-by-side
The project SHALL include a parity test suite that sends identical HTTP inputs to running Go and Rust server instances and compares their responses.

#### Scenario: HTTP endpoint parity is verified before cutover
- **WHEN** the parity test suite runs against both Go and Rust servers
- **THEN** every `/health`, `/v1/models`, routing error, and non-streaming chat stub response SHALL be byte-equivalent between Go and Rust

### Requirement: Benchmark workloads are defined and gated
The project SHALL define at least two benchmark workloads: concurrent SSE streaming chat proxy and `/v1/models` aggregation latency.

#### Scenario: Rust meets or beats Go on SSE throughput
- **WHEN** the benchmark runs N concurrent SSE streaming chat requests against both runtimes
- **THEN** the Rust runtime SHALL achieve equal or better throughput and p95 latency than Go

#### Scenario: Rust meets or beats Go on model-list response time
- **WHEN** the benchmark runs sequential `/v1/models` requests against both runtimes
- **THEN** the Rust runtime SHALL achieve equal or better median response time than Go

### Requirement: Cutover is blocked if any parity or benchmark gate fails
The deployment/retirement task SHALL NOT execute if the parity test suite or any benchmark threshold fails.

#### Scenario: Failing parity test blocks retirement
- **WHEN** any parity test produces a non-equivalent result
- **THEN** the cutover task SHALL exit non-zero and the Go source SHALL remain unchanged
