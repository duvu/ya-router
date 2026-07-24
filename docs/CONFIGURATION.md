# ya-router configuration guide

## Configuration source

The compatibility runtime reads:

```text
~/.local/share/github-copilot-svcs/config.json
```

Managed deployments normally set `YA_ROUTER_CONFIG_PATH` or
`YA_ROUTER_CONFIG_DIR`; the systemd package uses:

```text
/var/lib/ya-router/config.json
```

Configuration and routing state are written atomically with owner-only
permissions.

Start from `config.example.json` and inspect the effective state with:

```bash
./ya-router config
./ya-router status
```

## Application log files

The shared logger writes to stderr and to the configured local file:

```json
"logging": {
  "file_path": "logs/ya-router.log",
  "max_file_size_mib": 5,
  "retained_files": 2
}
```

The parent directory is created at startup. The default retains two files of at
most 5 MiB each. If file logging cannot be initialized, ya-router reports the
sanitized error to stderr and continues with console logging.

## Server and state environment

| Environment variable | Default | Purpose |
|---|---|---|
| `YA_ROUTER_LISTEN_ADDRESS` | `127.0.0.1` | Data-plane bind address |
| `YA_ROUTER_API_KEY` | empty | Inbound credential; required for non-loopback binding |
| `YA_ROUTER_CORS_ALLOWED_ORIGINS` | empty | Comma-separated browser-origin allowlist |
| `YA_ROUTER_CONFIG_PATH` | compatibility path | Explicit configuration file |
| `YA_ROUTER_CONFIG_DIR` | compatibility directory | Directory containing `config.json` |
| `YA_ROUTER_SECRETS_PATH` | beside managed config | Durable secret-reference state |
| `YA_ROUTER_OPERATIONS_PATH` | beside managed config | Durable control-operation state |
| `YA_ROUTER_COOLDOWNS_PATH` | beside managed config | Durable redacted per-target cooldown state |
| `YA_ROUTER_CODEX_OAUTH_CLIENT_ID` | Codex-compatible default | OAuth client override |
| `CODEX_HOME` | `~/.codex` | Read-only legacy Codex credential import |
| `OPENAI_API_KEY` | empty | OpenAI Platform API-key mode |
| `KILO_API_KEY` | empty | Authenticated Kilo Gateway access |
| `KILO_ORG_ID` | empty | Kilo organization context |
| `KILO_GATEWAY_BASE_URL` | `https://api.kilo.ai/api/gateway` | Kilo Gateway override |

A non-loopback listener without `YA_ROUTER_API_KEY` is rejected at startup.

## Top-level schema

```json
{
  "port": 7071,
  "config_version": 1,
  "enable_pprof": false,
  "logging": {},
  "routing": {},
  "providers": {},
  "timeouts": {}
}
```

`enable_pprof` exposes `/debug/pprof/` under the same inbound access policy.
Keep it disabled outside controlled diagnostics.

## Routing

### Resolution order

1. Exact `routing.model_map` key.
2. Bare `model_map` key after removing a recognized provider prefix.
3. Explicit provider prefix.
4. Exact configured virtual-model ID such as `thiendu`.
5. Ordinary provider-catalog discovery.
6. Configured default provider only when the request omitted `model`.

Explicit prefixes are authoritative:

| Prefix | Provider |
|---|---|
| `github/` | GitHub Copilot |
| `codex/` | OpenAI Codex |
| `kilo/` | Kilo AI Gateway |

An explicit route is pinned. It never silently moves to another provider or
model.

### Default Spark-first chain

Fresh configurations expose `thiendu` using `quota_priority`:

```json
{
  "routing": {
    "default_model": "thiendu",
    "default_provider": "copilot",
    "show_unavailable_models": false,
    "expose_internal_models": false,
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

Targets are canonical provider-prefixed catalog IDs. A target is selectable only
when its provider is enabled, authenticated/ready, capability-compatible,
allowed, present in the last-known-good catalog, and outside target cooldown.

### Virtual-model strategies

`priority`
: Backward-compatible behavior. The last successful target may be moved to the
  front for a later request while it remains routable.

`quota_priority`
: Configured order is authoritative for every new logical request. Active
  cooldowns are skipped. When a higher-priority cooldown expires, the next
  request evaluates that target before lower-priority targets again.

Validation requires a supported strategy, at least one unique target, known
provider prefixes, and no virtual-model nesting or `model_map` shadowing.

### Bounded sequential failover

Virtual-model attempts are sequential and bounded by the configured target
count. The same logical request may continue to the next target only when:

- no response output has been committed;
- the parent context/deadline is still active;
- another target is routable;
- the failure is eligible, such as quota/rate limit, auth or entitlement denial,
  pre-output timeout/transport failure, or `5xx`.

Each target is attempted at most once. All attempts share the original request
body, context, and deadline. Invalid client payloads, unsupported capabilities,
cancellation, explicit routes, and failures after output begins do not fail
over. Successful streaming remains incremental and is never buffered in full.

### Quota and transient cooldowns

Cooldown state is scoped by:

```text
(provider, upstream model, capability)
```

Exhausting Spark therefore does not disable `codex/gpt-5.4-mini` or another
capability.

- A quota-style response creates a cooldown of at least 24 hours.
- A trustworthy reset later than 24 hours is honored.
- A short trustworthy `Retry-After` on a burst `429` remains a short cooldown.
- Generic timeout, transport failure, and `5xx` retain bounded short cooldowns.
- After expiry, `quota_priority` restores configured order automatically.

The managed daemon persists cooldowns to `YA_ROUTER_COOLDOWNS_PATH`, normally:

```text
/var/lib/ya-router/cooldowns.json
```

The file is atomic and mode `0600`. It contains only bounded fields such as
provider, model, capability, stable reason, failure count, and UTC expiry. It
never contains prompts, completions, credentials, raw upstream bodies, or
account identifiers. Invalid advisory state is ignored with a sanitized warning
so it cannot prevent daemon startup.

### Model mappings

Use `model_map` for explicit aliases that must remain pinned:

```json
{
  "routing": {
    "model_map": {
      "research-model": {
        "provider": "codex",
        "upstream_model": "gpt-5.4-mini"
      }
    }
  }
}
```

### Model discovery

`show_unavailable_models` controls whether unauthenticated providers appear in
normal discovery.

`expose_internal_models` controls whether `GET /v1/models` exposes all
provider-prefixed models and mappings or only public virtual models and required
compatibility aliases. It does not disable explicit routing.

### Claude Code aliases

`claude_aliases` projects a Claude-compatible discovery name onto a canonical
Responses-capable target:

```json
{
  "routing": {
    "claude_aliases": {
      "claude-ya-codex-gpt-5-4-mini": "codex/gpt-5.4-mini"
    }
  }
}
```

The alias is advertised only while the target is discoverable and
Responses-capable.

## GitHub Copilot provider

```json
{
  "providers": {
    "copilot": {
      "enabled": true,
      "auth": {"mode": "device_code"},
      "accounts": [],
      "account_cooldown_seconds": 300,
      "allowed_models": []
    }
  }
}
```

Authenticate with:

```bash
./ya-router auth copilot
./ya-router auth copilot --account work
```

Provider-internal account rotation remains separate from virtual-model target
cooldown.

## Codex provider

### ChatGPT/Codex mode

```json
{
  "providers": {
    "codex": {
      "enabled": true,
      "auth": {"mode": "chatgpt"},
      "accounts": [],
      "account_cooldown_seconds": 300,
      "allowed_models": [],
      "chatgpt_base_url": "https://chatgpt.com/backend-api/codex/"
    }
  }
}
```

Authenticate with:

```bash
./ya-router auth codex
./ya-router auth codex --account work
```

ChatGPT mode supports chat and native Responses, not embeddings. A `401` gets
one refresh and one request retry. `$CODEX_HOME/auth.json` is a read-only
legacy/single-account fallback and never overrides a populated managed account
pool.

### OpenAI Platform API-key mode

```bash
export OPENAI_API_KEY=sk-...
```

or:

```bash
printf '%s\n' "$OPENAI_API_KEY" | ./ya-router auth codex --api-key-stdin
```

API-key mode supports chat, Responses, and embeddings and does not use ChatGPT
account metadata.

## Kilo Gateway provider

Kilo is disabled until explicitly enabled or authenticated:

```bash
./ya-router auth kilo
```

Example configuration:

```json
{
  "providers": {
    "kilo": {
      "enabled": true,
      "allow_anonymous": true,
      "organization_id": "",
      "base_url": "https://api.kilo.ai/api/gateway",
      "allowed_models": []
    }
  }
}
```

The canonical final fallback is:

```text
kilo/kilo-auto/free
```

Without a Kilo API key, routing is restricted to recognized free IDs. The
inbound ya-router credential is never forwarded upstream.

Auto Free may route through third parties that log prompts/outputs or use them
to improve services. Do not submit confidential, personal, or regulated data
through this fallback.

## Multi-account pools

Each provider account owns its authentication state. Provider-internal account
rotation is bounded by the account count. Target quota cooldown is applied only
after the provider returns the final redacted outcome to the router.

```json
{
  "providers": {
    "codex": {
      "enabled": true,
      "accounts": [
        {"label": "personal", "auth": {"mode": "chatgpt"}},
        {"label": "platform", "auth": {"mode": "api_key"}}
      ],
      "account_cooldown_seconds": 300
    }
  }
}
```

## Timeouts

Timeout values are seconds:

```json
{
  "timeouts": {
    "http_client": 300,
    "server_read": 30,
    "server_write": 300,
    "server_idle": 120,
    "proxy_context": 300,
    "circuit_breaker": 30,
    "keep_alive": 30,
    "tls_handshake": 10,
    "dial_timeout": 10,
    "idle_conn_timeout": 90
  }
}
```

All failover attempts share the original request deadline; failover never
extends it per target.

## Security invariants

- Non-loopback binding requires an inbound API key.
- Secrets are accepted through environment variables, stdin, or masked input.
- The Control API never returns raw secret values.
- Prompts, completions, credentials, raw upstream error bodies, and account IDs
  are excluded from routing logs and cooldown state.
- Kilo Auto Free is not appropriate for confidential or regulated data.
