# Umbrella Model Routing Architecture

Status: implemented

Related issues: #25, #96-#100, #112-#114  
Last updated: 2026-07-24

## 1. Decision summary

`ya-router` supports client-facing umbrella models, also called virtual models.
The default public model is `thiendu`. Fresh configurations use this ordered
quota-consumption chain:

```text
thiendu (`quota_priority`)
  1. codex/gpt-5.3-codex-spark
  2. codex/gpt-5.4-mini
  3. github/gpt-5.4-mini
  4. github/gpt-5-mini
  5. kilo/kilo-auto/free
```

The router keeps using the highest-priority routable target. When a target
returns a quota-style failure before output begins, the same logical request may
continue to the next eligible target. The exhausted provider/model/capability
target is then skipped for at least 24 hours. After expiry, configured order is
restored and the higher-priority target is evaluated again.

Attempts are sequential, bounded by the configured target count, and share the
original request body, context, and deadline. Once response output is committed,
the request is pinned and no additional target receives it.

## 2. Why this belongs in the router

Existing deterministic route mechanisms remain authoritative:

- `routing.model_map` for explicit aliases;
- explicit provider prefixes (`github/`, `codex/`, `kilo/`);
- ordinary provider-catalog discovery;
- a default provider only when `model` is omitted and no higher rule resolves.

A single mapping cannot express an ordered, availability-aware quota chain. The
virtual-model layer therefore owns target ordering, target availability, bounded
same-request failover, and cross-request cooldown state. Provider adapters still
own authentication refresh, provider-internal account rotation, protocol
translation, and upstream request execution.

## 3. Terminology

| Term | Meaning |
|---|---|
| Umbrella model | Client-facing ID configured under `routing.virtual_models` |
| Target | Canonical provider-prefixed upstream model ID |
| Routable | Eligible under provider readiness, capability, policy, catalog, and cooldown state |
| Attempt | One dispatch to one target inside a logical virtual-model request |
| Bounded failover | Sequentially selecting the next eligible target before output commit |
| Request pinning | No further target changes after response output is committed |
| Quota cooldown | Durable exclusion for one provider/model/capability until its retry time |
| Explicit route | `model_map` or provider-prefixed request that never enters virtual failover |

## 4. Goals

1. Present one stable client model ID.
2. Consume the highest-priority available model quota before moving down the
   configured chain.
3. Remember exhausted targets for at least 24 hours across hot reloads and daemon
   restarts.
4. Keep failover sequential, deterministic, bounded, and safe before output
   commit.
5. Preserve explicit-route pinning and provider isolation.
6. Keep routing state and diagnostics redacted and bounded.
7. Reuse existing router, provider, cooldown, runtime snapshot, telemetry, and
   proxy paths rather than introducing a workflow engine.

## 5. Non-goals

- Parallel hedging or speculative duplicate generations.
- Continuing with another target after partial output was sent.
- Weighted, random, latency, price, or quality scoring.
- Prompt inspection or LLM-based routing.
- Virtual models referencing other virtual models.
- Persisting prompts, completions, raw upstream errors, credentials, or account
  identifiers.
- Silently rerouting explicit provider-prefixed or mapped requests.

## 6. Configuration contract

```json
{
  "routing": {
    "default_model": "thiendu",
    "virtual_models": {
      "thiendu": {
        "strategy": "quota_priority",
        "targets": [
          "codex/gpt-5.3-codex-spark",
          "codex/gpt-5.4-mini",
          "github/gpt-5.4-mini",
          "github/gpt-5-mini",
          "kilo/kilo-auto/free"
        ]
      }
    }
  }
}
```

### 6.1 Strategies

`priority`
: Backward-compatible strategy. The last successful target may be moved to the
  front on later requests while it remains routable.

`quota_priority`
: Configured order is authoritative for each new request. Active cooldowns are
  skipped. When a higher-priority cooldown expires, the target automatically
  returns ahead of lower-priority targets.

### 6.2 Validation

- Strategy must be supported.
- At least one target is required.
- Targets must be non-empty, unique, and provider-prefixed.
- Targets cannot reference another virtual model.
- A virtual ID cannot collide with a provider namespace or shadow a
  `model_map` entry.

## 7. Routing precedence

1. Exact `model_map` entry.
2. Bare `model_map` match after removing a recognized prefix.
3. Explicit provider prefix.
4. Exact virtual-model ID.
5. Ordinary provider-catalog discovery.
6. Default provider only for an omitted model when no higher rule resolves.

Consequences:

- `github/*`, `codex/*`, and `kilo/*` stay pinned.
- Explicit aliases stay pinned.
- Only an exact configured virtual ID enters automatic target selection and
  bounded failover.

## 8. Target availability

A target is routable for one capability only when:

1. its provider exists in the immutable runtime snapshot;
2. the provider supports the requested capability;
3. provider health is authenticated/ready;
4. the last-known-good catalog contains the target model;
5. allowlists, entitlement, and anonymous/free rules permit the target;
6. configuration has not disabled the target/provider;
7. no active cooldown exists for the same provider/model/capability;
8. the target has not already been attempted in the current logical request.

A present but stale catalog remains routable and is flagged diagnostically. A
missing catalog fails closed. Availability evaluation performs no network I/O on
the request hot path.

## 9. Selection algorithm

```text
resolve(virtual model, capability, excluded targets)
  choose target order according to strategy
  for target in order:
    skip if already attempted
    skip if unavailable or cooling down
    return target and bounded decision metadata
  return no_active_target
```

For `priority`, preferred-target memory may change the starting order. For
`quota_priority`, configured order is never changed by prior success.

Decision metadata includes the virtual ID, selected target, configured index,
strategy, capability, runtime generation, catalog state, and stable skip reason
codes. It contains no provider-supplied prose.

## 10. Bounded same-request failover

The proxy may continue to another target only when all conditions hold:

- the original route is a virtual model;
- response output has not been committed;
- the parent request context is still active;
- another target is routable;
- the failure is eligible.

Eligible pre-output outcomes include:

- quota or rate limit;
- authentication required;
- payment/entitlement denial;
- timeout while the parent context remains usable;
- `5xx`;
- provider or transport error before output begins.

The proxy does not fail over for:

- invalid client payloads or ordinary `400` responses;
- unsupported capability or unknown model;
- explicit routes;
- client cancellation or expired parent context;
- any error after response bytes are committed.

Each target is attempted at most once. Maximum attempts equal the configured
target count. All attempts reuse the bounded original request body and overall
deadline. Successful streaming switches to pass-through output and is not
buffered in full.

## 11. Cooldown policy

Cooldown identity is:

```text
(provider, upstream model, capability)
```

This prevents one exhausted model from disabling unrelated targets in the same
provider.

### 11.1 Quota cooldown

A quota-style outcome creates a cooldown of at least 24 hours. If a trustworthy
upstream reset is later than 24 hours, the later reset is used. The default
`quota_priority` chain therefore consumes Spark until exhaustion, then moves to
Codex GPT-5.4 mini, then Copilot targets, and finally Kilo Auto Free. Each target
has independent state.

### 11.2 Transient cooldown

A short trustworthy `Retry-After` on burst throttling, generic timeouts,
transport failures, and `5xx` use bounded short cooldowns rather than locking a
target for a full day.

### 11.3 Recovery

Expired entries are removed lazily during availability reads. After a
higher-priority quota cooldown expires, `quota_priority` selects it before lower
targets on the next request.

## 12. Durable state

Managed systemd deployments set:

```text
YA_ROUTER_COOLDOWNS_PATH=/var/lib/ya-router/cooldowns.json
```

The registry is shared across immutable runtime snapshots and persisted
atomically with mode `0600`. State contains only bounded fields:

- provider;
- upstream model;
- capability;
- stable cooldown reason;
- UTC expiry;
- bounded failure count.

It excludes prompts, completions, credentials, raw upstream bodies, and account
identifiers. Invalid JSON, unsupported versions, expired entries, and invalid
records are ignored safely with sanitized logging so advisory state cannot block
daemon startup.

## 13. Observability

Routing logs and metrics report stable values only:

```text
provider_not_registered
provider_not_ready
capability_unsupported
model_not_in_catalog
model_disallowed
target_disabled
cooldown
already_attempted
```

Cooldown reasons are similarly bounded:

```text
quota_exhausted
rate_limited
auth_required
entitlement_denied
timeout
transient_failure
```

The WebSocket/TUI path reuses the same proxy and emits a route event for each
attempt, updating the in-progress assistant entry rather than creating duplicate
messages.

## 14. Security and data handling

- Explicit routes never cross providers automatically.
- Cooldown and telemetry state contain no request content or secrets.
- Successful output from multiple providers is never mixed.
- Kilo Auto Free may route through third parties; confidential, personal, or
  regulated data must not be submitted through that final fallback.

## 15. Verification contract

The quality gate requires:

```text
gofmt verification
go vet ./...
go test -race -count=1 ./...
build ya-router, ya-routerd, and ya
Docker compatibility image build
```

Release validation additionally verifies Linux/amd64 checksums, build metadata,
managed-state backup, service readiness, persisted cooldown permissions, and
sanitized real-daemon smoke scenarios.
