# Copilot Free Model Rotation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Force Copilot chat traffic to rotate only across models that are both Copilot-Free-available and zero-premium, with automatic failover to the next eligible model when a selected model fails.

**Architecture:** Add a docs-backed free-model catalog with disk cache, match docs models against the live Copilot `/models` list, and route all chat requests through a Copilot-managed round-robin selector with same-request failover. Embeddings keep the existing router path.

**Tech Stack:** Go, net/http, existing provider registry/router/proxy stack, table parsing from GitHub Docs HTML, Go test

---

### Task 1: Add failing tests for docs parsing and eligibility computation

**Files:**
- Create: `src/copilot_free_catalog_test.go`
- Modify: `src/helpers_test.go`

- [ ] **Step 1: Write failing tests for docs table parsing**
- [ ] **Step 2: Run `go test ./src -run 'TestCopilotFreeCatalog'` and verify failure**
- [ ] **Step 3: Implement minimal parser helpers and eligibility computation**
- [ ] **Step 4: Re-run `go test ./src -run 'TestCopilotFreeCatalog'` and verify pass**

### Task 2: Add failing tests for free-model selection and request failover

**Files:**
- Modify: `src/proxy_test.go`
- Modify: `src/helpers_test.go`

- [ ] **Step 1: Write failing tests for chat model rotation, client-model ignore, and failover**
- [ ] **Step 2: Run `go test ./src -run 'TestProcessProxyRequest_|TestCopilotFree'` and verify failure**
- [ ] **Step 3: Implement selector integration in the Copilot chat path**
- [ ] **Step 4: Re-run `go test ./src -run 'TestProcessProxyRequest_|TestCopilotFree'` and verify pass**

### Task 3: Implement docs-backed catalog, cache, and upstream matching

**Files:**
- Create: `src/copilot_free_catalog.go`
- Modify: `src/transform.go`
- Modify: `src/copilot_provider.go`
- Modify: `src/model_cache.go`

- [ ] **Step 1: Add runtime structs for docs snapshots, cache persistence, and upstream model metadata**
- [ ] **Step 2: Implement docs fetch, HTML table extraction, normalization, and cache load/save**
- [ ] **Step 3: Match docs-eligible names against live Copilot models and build the effective ring**
- [ ] **Step 4: Re-run targeted tests and fix mismatches**

### Task 4: Wire proxy behavior, docs, and verification

**Files:**
- Modify: `src/proxy.go`
- Modify: `README.md`
- Modify: `config.example.json`

- [ ] **Step 1: Force chat requests to use Copilot free rotation and preserve embeddings behavior**
- [ ] **Step 2: Update docs/config examples to explain that Copilot chat ignores client `model` and rotates across free models**
- [ ] **Step 3: Run `go test ./...`**
- [ ] **Step 4: Run `go run ./src status` or another lightweight sanity command if needed**
