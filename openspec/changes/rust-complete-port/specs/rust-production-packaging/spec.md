## ADDED Requirements

### Requirement: Rust binary has a dedicated Dockerfile
The project SHALL include `Dockerfile.rust` implementing a multi-stage build: rust builder stage compiling `github-copilot-svcs-rs`, then a minimal runtime image.

#### Scenario: Rust Docker image builds successfully
- **WHEN** `docker build -f Dockerfile.rust .` is run
- **THEN** it SHALL produce a runnable image exposing port 7071 with the binary at `/app/github-copilot-svcs-rs`

### Requirement: Rust binary has Makefile build/run/push targets
The Makefile SHALL include `rust-docker-build` and `rust-docker-run` targets in addition to the existing `rust-build/test/check` targets.

#### Scenario: Makefile rust-docker-build builds Rust image
- **WHEN** `make rust-docker-build` is run
- **THEN** it SHALL invoke `docker build -f Dockerfile.rust` and produce a tagged image

### Requirement: CI job validates Rust build and tests
The `.github/workflows/ci-cd.yml` SHALL include a job that runs `cargo test` and `cargo build --release` for the Rust binary on every push.

#### Scenario: CI runs Rust tests in parallel with Go tests
- **WHEN** a commit is pushed to any branch
- **THEN** the CI workflow SHALL run the Rust test job alongside the existing Go job without blocking each other
