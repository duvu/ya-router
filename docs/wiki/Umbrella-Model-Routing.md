# Umbrella Model Routing

> Status: planned. Runtime support is tracked by [epic #25](https://github.com/duvu/ya-router/issues/25) and is not implemented yet.

## What is an umbrella model?

An umbrella model is one stable client-facing model ID that points to an ordered list of real provider/model targets.

Example:

```text
router/auto
  1. github/gpt-5-mini
  2. codex/gpt-5.4-mini
  3. kilo/kilo-auto/free
```

When a request uses `router/auto`, `ya-router` selects the first target that is currently active for the requested endpoint capability.

## This is not cross-provider failover

The router selects one target before sending the request. That target remains pinned for the complete request.

If the selected target returns `401`, `403`, `429`, `5xx`, a timeout, or a transport error, the error is returned to the client. The same request is not retried against another target.

A later request may select a different target if provider health or model availability has changed.

## Planned configuration

```json
{
  "routing": {
    "virtual_models": {
      "router/auto": {
        "strategy": "priority",
        "targets": [
          "github/gpt-5-mini",
          "codex/gpt-5.4-mini",
          "kilo/kilo-auto/free"
        ]
      }
    }
  }
}
```

V1 is intentionally small:

- strategy: `priority` only;
- target order is priority order;
- every target uses a provider prefix;
- umbrella models cannot reference other umbrella models;
- no weighted, random, cost, latency, quality, or prompt-based selection.

## When is a target active?

A target is active only when:

- its provider is present in the current runtime snapshot;
- the provider is ready;
- the provider supports the requested capability;
- the target model exists in the accepted last-known-good catalog;
- provider allowlist, entitlement, and anonymous/free rules permit it.

Selection reads cached/atomic availability state. It does not make an upstream catalog request on the request hot path.

## Routing priority

Planned routing order:

1. exact `routing.model_map`;
2. explicit `github/*`, `codex/*`, or `kilo/*` prefix;
3. exact configured umbrella model;
4. ordinary provider catalog discovery;
5. default provider only when the model field was omitted.

Explicit prefixed models never enter umbrella routing.

## Backlog

| Order | Issue | Scope |
|---:|---|---|
| 1 | [#26](https://github.com/duvu/ya-router/issues/26) | Contract and configuration schema |
| 2 | [#27](https://github.com/duvu/ya-router/issues/27) | Target availability snapshots |
| 3 | [#28](https://github.com/duvu/ya-router/issues/28) | Deterministic priority selector |
| 4 | [#29](https://github.com/duvu/ya-router/issues/29) | Data-plane and `/v1/models` integration |
| 5 | [#30](https://github.com/duvu/ya-router/issues/30) | Observability and diagnostics |
| 6 | [#31](https://github.com/duvu/ya-router/issues/31) | Control API, CLI, and TUI workflows |
| 7 | [#32](https://github.com/duvu/ya-router/issues/32) | Production and regression gates |

## Detailed documents

- [Architecture](../architecture/umbrella-model-routing.md)
- [Delivery roadmap](../roadmaps/umbrella-model-routing-roadmap.md)
