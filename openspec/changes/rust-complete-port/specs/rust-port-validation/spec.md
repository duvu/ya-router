## MODIFIED Requirements

### Requirement: Rust port includes parity validation gates
The system SHALL define and execute parity validation between the Go and Rust runtimes before the Rust implementation is considered ready for primary use.

#### Scenario: Shared behavior checks
- **WHEN** the Rust runtime is proposed for readiness
- **THEN** there SHALL be an executable validation suite (not just planned) covering endpoint behavior, CLI behavior, config migration, provider auth flows, routing decisions, and model exposure

#### Scenario: Cutover blocked on parity failures
- **WHEN** parity validation finds a behavioral mismatch between the Go and Rust runtimes
- **THEN** the Rust runtime is NOT promoted and the cutover task exits non-zero

### Requirement: Rust port includes benchmark success criteria
The system SHALL define benchmark scenarios and success thresholds that justify the Rust port's performance objective.

#### Scenario: Benchmark workload definition
- **WHEN** implementation planning is completed
- **THEN** benchmark scenarios SHALL include at minimum: (1) N concurrent SSE streaming chat proxy requests and (2) sequential `/v1/models` aggregation latency, with numeric p95 latency and throughput thresholds defined in `rust/benches/`

#### Scenario: Benchmark-based adoption decision
- **WHEN** the Rust runtime is evaluated for adoption
- **THEN** the decision includes measured latency, throughput, and resource-usage comparisons against the current Go runtime; the Rust runtime SHALL meet or beat Go on all defined thresholds

### Requirement: Rust port uses phased rollout and rollback planning
The system SHALL define a migration path that allows phased adoption of the Rust runtime and straightforward rollback to the Go runtime.

#### Scenario: Side-by-side runtime period
- **WHEN** the Rust runtime first becomes runnable
- **THEN** the migration plan supports a period where the Go runtime remains available as the behavioral reference and rollback target

#### Scenario: Deployment continuity
- **WHEN** packaging and CI/CD are updated for the Rust runtime
- **THEN** the rollout plan preserves the repository's existing production deployment controls until Rust readiness is proven, at which point a single gated retirement task removes Go
