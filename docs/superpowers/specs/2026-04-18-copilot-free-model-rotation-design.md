# Copilot Free Model Rotation Design

**Date:** 2026-04-18

## Goal

Force GitHub Copilot chat traffic to use only models that are both:

- available on Copilot Free
- zero-premium on paid Copilot plans

The service, not the client, chooses the model. If one selected model fails before a response is committed, the same request immediately shifts to the next eligible free model.

## Non-Goals

- Changing embeddings routing
- Preserving client-selected chat model IDs
- Supporting non-official metadata sources for free/non-premium status

## Trust Source

The service derives the eligible Copilot chat pool from official GitHub Docs:

- `https://docs.github.com/en/copilot/reference/ai-models/supported-models`
- `https://docs.github.com/en/copilot/concepts/billing/copilot-requests`

The docs pages are parsed from rendered HTML because the public markdown API currently strips the generated table rows needed for plan availability and multiplier data.

The service stores a last-known-good catalog snapshot on disk. On startup it loads the cached snapshot first, then refreshes in the background or lazily on demand. Refresh failures never discard a valid cached snapshot.

## Eligibility Rule

An upstream Copilot model is eligible for chat rotation only if:

1. the docs mark the model as available on `Copilot Free`
2. the docs mark the model as `0` premium on paid plans
3. the model is actually present in the current Copilot `/models` response
4. the model resolves to a visible or otherwise best upstream candidate after name normalization and alias matching

For current docs this yields models such as `GPT-4.1`, `GPT-4o`, `GPT-5 mini`, and `Raptor mini`, subject to live upstream availability.

## Architecture

### 1. Docs Catalog

A new catalog component:

- fetches the two GitHub Docs HTML pages
- extracts the relevant tables
- normalizes model names
- computes the docs-side eligible set
- persists a cached snapshot with fetch time and source URLs

### 2. Upstream Matching

The Copilot provider already fetches `/models`. The new logic uses the raw upstream model list, not `allowed_models`, because chat eligibility must be decided dynamically by the service.

Doc names are matched to upstream models using normalized `name`, `id`, and version-aware aliases, with preference for:

- exact visible picker models
- exact name matches
- non-preview variants when possible

### 3. Rotation

The Copilot provider maintains an in-memory round-robin cursor over the current eligible pool. Every chat request starts from the next position in the ring.

### 4. Failover

Chat requests try the selected model first. On transport failure or retryable upstream error before anything is written to the client, the service patches the request body with the next eligible model and retries the same request against Copilot. This continues until one model succeeds or the pool is exhausted.

### 5. Routing Behavior

- Chat: ignore client `model`, bypass normal model selection, force Copilot free-pool selection
- Embeddings: keep existing routing behavior unchanged

## Error Handling

- If the docs refresh fails but a cached snapshot exists, continue with cache
- If no cached snapshot exists and docs cannot be fetched, fail closed for Copilot chat
- If no eligible upstream model remains after docs/upstream intersection, fail closed for Copilot chat
- If the chosen model fails with a failover-worthy error, shift immediately to the next eligible model
- If all eligible models fail, return the final upstream error context to the client
- Mid-stream failures after a `200` response has already started cannot be transparently retried

## Testing

Tests cover:

- parsing supported-plan and multiplier tables from docs HTML
- computing the docs-side eligible set
- resolving docs names to upstream Copilot models
- round-robin rotation order
- request failover to the next eligible model
- ignoring client-supplied chat model IDs
- preserving embeddings behavior
