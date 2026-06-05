## 1. Remove Rust Workspace

- [x] 1.1 Delete the `rust/` directory (entire Rust workspace including `Cargo.toml`, `src/`, `Cargo.lock` if present, and any generated `target/` artifacts)
- [x] 1.2 Remove rust-* Makefile targets: `rust-build`, `rust-test`, `rust-fmt`, `rust-check`, `rust-run` plus their `.PHONY` entries and the help-text lines that reference them

## 2. Remove AllowedModels Gate from Providers

- [x] 2.1 In `src/copilot_provider.go` `ListModels`: remove the `filterAllowedModels(raw, p.cfg.Providers.Copilot.AllowedModels)` call; return `raw` directly
- [x] 2.2 In `src/codex_provider.go` `ListModels`: remove the `filterAllowedModels` call; return the full model list directly

## 3. Update Tests

- [x] 3.1 In `src/proxy_test.go` `TestFilterAllowedModels`: this tests the helper function directly and remains valid; verify it still passes unchanged
- [x] 3.2 Review `src/integration_test.go` and `src/proxy_test.go` for any tests that set `allowed_models` and assert a filtered model count from a provider's `ListModels`; update those to expect the full unfiltered list
- [x] 3.3 Run `make test` and fix any test failures caused by the provider ListModels change

## 4. Update Documentation and Config Example

- [x] 4.1 In `config.example.json`: empty the `allowed_models` arrays (set back to `[]`) since populated arrays now have no effect on model listing; keep the field present for schema compatibility
- [x] 4.2 In `docs/CONFIGURATION.md`: update the `allowed_models` section to state that the field is accepted by the config parser but no longer gates the model list; models from each provider are always exposed in full

## 5. Verification

- [x] 5.1 Run `make fmt && make vet && make test && make build`; confirm all pass with zero new failures
- [x] 5.2 Confirm `ls rust/` returns "no such file" to verify Rust workspace is fully removed
- [x] 5.3 Confirm `make rust-build` returns an error ("no rule to make target") to verify Makefile targets are removed
