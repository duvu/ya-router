# Configuration Guide

`github-copilot-svcs` is configured via a single JSON file at:

```
~/.local/share/github-copilot-svcs/config.json
```

The service automatically migrates older config versions on startup. Use `config.example.json` in the repo root as a starting point.

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [GitHub Copilot Setup](#github-copilot-setup)
3. [Copilot Multi-Account Pool](#copilot-multi-account-pool)
4. [OpenAI Codex Setup](#openai-codex-setup)
5. [Codex Multi-Account Pool](#codex-multi-account-pool)
6. [Model IDs and Prefixes](#model-ids-and-prefixes)
7. [Routing and Model Map](#routing-and-model-map)
8. [Provider Filtering with allowed_models](#provider-filtering-with-allowed_models)
9. [Timeouts](#timeouts)
10. [Troubleshooting](#troubleshooting)

---

## Quick Start

```bash
# Authenticate with GitHub Copilot
github-copilot-svcs auth copilot

# Start the proxy
github-copilot-svcs run

# List available models
github-copilot-svcs models
```

The proxy listens on port **7071** by default and exposes:

| Path | Method | Description |
|------|--------|-------------|
| `/v1/models` | GET | List available models |
| `/v1/chat/completions` | POST | Chat completions (OpenAI-compatible) |
| `/v1/embeddings` | POST | Embeddings (OpenAI-compatible) |
| `/health` | GET | Health check |

---

## GitHub Copilot Setup

GitHub Copilot uses OAuth device-code flow. No API key is required.

### Authentication

```bash
github-copilot-svcs auth copilot
```

Follow the browser prompt to authorise. Credentials are stored in the runtime config file. Tokens refresh automatically.

### Configuration

```json
"providers": {
  "copilot": {
    "enabled": true,
    "auth": {
      "mode": "device_code"
    },
    "allowed_models": []
  }
}
```

**`enabled`** — Set to `true` to activate the Copilot provider.

**`auth.mode`** — Always `"device_code"` for Copilot.

**`allowed_models`** — Accepted by the config parser for schema compatibility, but no longer gates the model list. All models returned by the Copilot API are always exposed. Leave as `[]`.

### Notes

- Copilot chat **ignores the `model` field** from the client. The service rotates across an eligible free-tier pool and falls back on error. This is intentional Copilot behaviour.
- Embeddings use normal model routing.
- Run `github-copilot-svcs refresh --provider copilot` to force a token refresh.

---

## Copilot Multi-Account Pool

Run multiple Copilot accounts to reduce rate-limit impact. When all model attempts on the active account return 429 or 403 (rate limit), the service automatically advances to the next healthy account.

### Adding a second account

```bash
github-copilot-svcs auth copilot --account work
github-copilot-svcs auth copilot --account personal
```

Each `--account` label is stored as an entry in `providers.copilot.accounts`. Omitting `--account` authenticates the first account (label `"primary"`).

### Config format

```json
"providers": {
  "copilot": {
    "enabled": true,
    "accounts": [
      { "label": "primary", "auth": { "mode": "device_code" } },
      { "label": "work",    "auth": { "mode": "device_code" } }
    ],
    "account_cooldown_seconds": 300
  }
}
```

**`accounts`** — List of Copilot accounts. Leave empty for single-account mode; the service auto-promotes the legacy `auth` field on first load.

**`account_cooldown_seconds`** — How long (in seconds) a rate-limited account is skipped before being retried. Default `300` (5 min).

### Single-account backward compatibility

Existing configs with `providers.copilot.auth.github_token` are automatically promoted to `accounts[0]` (label `"primary"`) at load time. No manual migration is required.

### Viewing pool status

```bash
github-copilot-svcs status
```

Shows per-account auth state and cooldown remaining when 2 or more accounts are configured.

---

## OpenAI Codex Setup

Codex supports two authentication modes:

| Mode | Credential source | Use when |
|------|------------------|----------|
| `device_code` / `chatgpt` | ChatGPT account (device OAuth) | You have a ChatGPT Plus/Teams account |
| `api_key` | `OPENAI_API_KEY` env var or config | You have an OpenAI Platform API key |

### ChatGPT / Device-code authentication

```bash
github-copilot-svcs auth codex
```

This runs the OpenAI device-code OAuth flow. Credentials are persisted in the official Codex auth store at `~/.codex/auth.json`. The proxy config retains only `mode` and `enabled`; no bearer tokens are written to the config file.

```json
"providers": {
  "codex": {
    "enabled": true,
    "auth": {
      "mode": "device_code"
    },
    "allowed_models": []
  }
}
```

### API key authentication

Set the environment variable before starting the service:

```bash
export OPENAI_API_KEY=sk-...
github-copilot-svcs run
```

Or configure `api_key_env` to name an alternate environment variable:

```json
"providers": {
  "codex": {
    "enabled": true,
    "auth": {
      "mode": "api_key",
      "api_key_env": "OPENAI_API_KEY"
    },
    "allowed_models": []
  }
}
```

**`enabled`** — Set to `true` to activate the Codex provider.

**`auth.mode`** — `"device_code"` for ChatGPT-backed auth, `"api_key"` for OpenAI Platform key.

**`auth.api_key_env`** — Only used in `api_key` mode. Names the environment variable that holds the key. Defaults to `OPENAI_API_KEY`.

**`allowed_models`** — Accepted by the config parser for schema compatibility, but no longer gates the model list. All Codex models are always exposed. Leave as `[]`.

### Notes

- Run `github-copilot-svcs auth codex --api-key sk-...` to store an API key in the config instead of using the environment variable.
- Run `github-copilot-svcs refresh --provider codex` to refresh a ChatGPT-backed token.
- The `status` command shows which credential source is active and whether the token is expired.

---

## Codex Multi-Account Pool

Run multiple Codex accounts to reduce rate-limit impact. When all request attempts on the active account return 429 or 403 (rate limit), the service automatically advances to the next healthy account.

### Adding accounts

```bash
# ChatGPT / device-code accounts
github-copilot-svcs auth codex --account personal
github-copilot-svcs auth codex --account work

# API key accounts
github-copilot-svcs auth codex --api-key sk-... --account platform-key
```

Each `--account` label is stored as an entry in `providers.codex.accounts`. Omitting `--account` authenticates or updates the first account (label `"primary"`).

### Config format

```json
"providers": {
  "codex": {
    "enabled": true,
    "accounts": [
      {
        "label": "personal",
        "auth": { "mode": "chatgpt" }
      },
      {
        "label": "platform-key",
        "auth": { "mode": "api_key", "api_key_env": "OPENAI_API_KEY_2" }
      }
    ],
    "account_cooldown_seconds": 300
  }
}
```

**`accounts`** — List of Codex accounts. Leave empty for single-account mode; the service auto-promotes the legacy `auth` field on first load.

**`account_cooldown_seconds`** — How long (in seconds) a rate-limited account is skipped before being retried. Default `300` (5 min).

### Credential storage

- **ChatGPT / device-code accounts**: credentials for each account are stored in the proxy config under that account's `auth` pool entry. The official Codex auth store (`~/.codex/auth.json`) is used as the initial auth-handshake channel but pool entries are persisted in config.
- **API key accounts**: the key is stored in the account's `auth.api_key` field in config, or resolved at runtime from `auth.api_key_env`.

### Single-account backward compatibility

Existing configs with `providers.codex.auth` populated are automatically promoted to `accounts[0]` (label `"primary"`) at load time. No manual migration is required.

### Viewing pool status

```bash
github-copilot-svcs status
```

Shows per-account authentication state and cooldown remaining when 2 or more accounts are configured.

---

## Model IDs and Prefixes

Every model exposed by `/v1/models` carries a provider-namespace prefix so clients can target a specific backend deterministically.

| Provider | Prefix | Example model ID |
|----------|--------|-----------------|
| GitHub Copilot | `gc-` | `gc-gpt-4o`, `gc-claude-3.5-sonnet` |
| OpenAI Codex | `oc-` | `oc-gpt-5.3-codex`, `oc-o3-mini` |

**Client usage:**

```bash
curl http://localhost:7071/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gc-gpt-4o","messages":[{"role":"user","content":"hello"}]}'
```

**Router behaviour:**

- A prefixed model ID like `gc-gpt-4o` is resolved to the Copilot provider, which then routes to `gpt-4o` upstream.
- A bare model ID like `gpt-4o` is accepted for backward compatibility. The router will try to discover which provider exposes it. If both providers expose the same bare ID, you must disambiguate with a prefix or a `model_map` entry.
- The prefix is stripped before the request is forwarded upstream, so upstream services always see the bare model name.

---

## Routing and Model Map

The `routing` block controls how models are resolved to providers.

```json
"routing": {
  "default_model": "gpt-5-mini",
  "default_provider": "copilot",
  "show_unavailable_models": false,
  "model_map": {
    "text-embedding-3-large": {
      "provider": "codex"
    },
    "gc-gpt-4o": {
      "provider": "copilot",
      "upstream_model": "gpt-4o"
    },
    "oc-gpt-5.3-codex": {
      "provider": "codex",
      "upstream_model": "gpt-5.3-codex"
    }
  }
}
```

**`default_model`** — The model used when a client sends a request without a `model` field. Bare ID; the router resolves it through the default provider.

**`default_provider`** — The provider used when no model is specified and no `model_map` entry matches.

**`show_unavailable_models`** — When `true`, models from unauthenticated providers still appear in `/v1/models`. Useful for debugging.

**`model_map`** — Explicit routing rules. Each key is the model ID clients use; the value specifies which provider to route to and optionally the upstream model name to send.

`model_map` has the highest routing priority. Use it to:
- Pin a specific model to a specific provider.
- Map a custom name to an upstream model.
- Resolve ambiguity when both providers expose a model with the same bare name.

**`upstream_model`** — Optional. If omitted, the key itself (after stripping any known prefix) is sent as the model name to the upstream.

### Resolution order

1. Exact `model_map` key match.
2. `model_map` bare-key match (key without prefix).
3. If the request uses a prefixed model ID (`gc-*` or `oc-*`), route to that provider's catalog.
4. Discover across provider catalogs. If exactly one provider exposes the model, route there.
5. If multiple providers expose the same model, return an error asking for disambiguation.
6. If no model was requested, use `default_provider`.

---

## Provider Filtering with allowed_models

> **Note:** `allowed_models` no longer gates the model list exposed by providers. The field is accepted by the config parser for backward compatibility but has no effect on which models appear at `/v1/models`. All models from each provider are always returned in full.

The `allowed_models` field may still appear in your config file. It will not cause errors and will be ignored during model listing.

```json
"providers": {
  "copilot": {
    "allowed_models": []
  },
  "codex": {
    "allowed_models": []
  }
}
```

To control which models a client can use, rely on `routing.model_map` to define explicit routes, or use the provider-prefixed model IDs (`gc-*` for Copilot, `oc-*` for Codex) to target a specific provider deterministically.

---

## Timeouts

All values are in seconds.

```json
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
```

| Key | Default | Description |
|-----|---------|-------------|
| `http_client` | 300 | Total timeout for outbound HTTP requests |
| `server_read` | 30 | Max time to read the full client request |
| `server_write` | 300 | Max time to write the full response (covers streaming) |
| `server_idle` | 120 | Keep-alive idle timeout for client connections |
| `proxy_context` | 300 | Context deadline for a full proxy round-trip |
| `circuit_breaker` | 30 | Time before a tripped circuit breaker retries |
| `keep_alive` | 30 | TCP keep-alive interval for outbound connections |
| `tls_handshake` | 10 | TLS handshake timeout |
| `dial_timeout` | 10 | TCP dial timeout for outbound connections |
| `idle_conn_timeout` | 90 | Max idle time for a pooled outbound connection |

For streaming responses, `server_write` and `proxy_context` should be large enough to cover the full stream duration.

---

## Troubleshooting

**`/v1/models` returns an empty list**

- Check `github-copilot-svcs status` to verify authentication.
- Run `github-copilot-svcs auth copilot` or `auth codex` to re-authenticate.
- Set `show_unavailable_models: true` temporarily to see models from unauthenticated providers.

**Requests fail with "model unavailable"**

- The model ID may not be exposed by any enabled provider. Run `github-copilot-svcs models` to list available models.
- Use a prefixed ID (`gc-*` or `oc-*`) to target a specific provider.
- Add an explicit `model_map` entry to route the model.

**Ambiguity error: "model X is available from multiple providers"**

- Use the prefixed form (`gc-X` or `oc-X`) in the request.
- Or add a `model_map` entry to pin the model to one provider.

**Token expired / auth errors**

- Run `github-copilot-svcs refresh` to refresh tokens.
- For Codex ChatGPT mode, re-run `github-copilot-svcs auth codex` if refresh fails.
- For Codex API key mode, verify the `OPENAI_API_KEY` environment variable is set.

**Config not found / defaults used**

- The config file is created automatically after the first `auth` command.
- Copy `config.example.json` to `~/.local/share/github-copilot-svcs/config.json` to pre-populate it.
- Run `github-copilot-svcs migrate-config --mode merge` to apply any new default fields.
