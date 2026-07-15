# Control API foundation

`ya-routerd` serves management traffic separately from the OpenAI-compatible
data plane. The initial Control API exposes only discovery and negotiation:

```text
GET /control/v1/meta
```

Provider, model, operation, authentication, and configuration resources are
added by the later managed-service roadmap issues. The current endpoint is the
stable foundation for those resources.

## Local transport

A private Unix socket is enabled by default next to the configured state file:

```text
<config-directory>/control.sock
```

The socket is created with mode `0600`. A systemd package may override the path
with `YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock` and manage group
access in the unit/package layer. Set the variable to `off` only when a valid
remote listener is configured; disabling every control listener fails startup.

Local socket access maps to the `admin` role because filesystem ownership and
socket permissions form the trust boundary. The Control API still rejects an
explicit `YA_ROUTER_API_KEY` presented as a control credential.

Example with curl:

```bash
curl --unix-socket ~/.local/share/github-copilot-svcs/control.sock \
  -H 'X-YA-Client-Version: 1.1.0' \
  http://unix/control/v1/meta
```

## Optional loopback TLS

A loopback TCP listener is opt-in and always uses HTTPS:

```bash
export YA_ROUTER_CONTROL_LISTEN_ADDRESS=127.0.0.1:7443
export YA_ROUTER_CONTROL_TLS_CERT=/etc/ya-router/control-server.pem
export YA_ROUTER_CONTROL_TLS_KEY=/etc/ya-router/control-server-key.pem
export YA_ROUTER_CONTROL_ADMIN_TOKEN="$(openssl rand -hex 32)"
```

Independent token variables are available for `viewer`, `operator`, and
`admin`:

```text
YA_ROUTER_CONTROL_VIEWER_TOKEN
YA_ROUTER_CONTROL_OPERATOR_TOKEN
YA_ROUTER_CONTROL_ADMIN_TOKEN
```

These values must differ from `YA_ROUTER_API_KEY`. Control tokens are a
loopback TLS bootstrap mechanism; they are not accepted as a replacement for
mTLS on a non-loopback listener.

## Non-loopback remote administration

A non-loopback control listener fails closed unless all of the following are
configured:

- server TLS certificate and private key;
- client CA trust file;
- mandatory verified client certificates;
- at least one certificate subject-to-role mapping.

```bash
export YA_ROUTER_CONTROL_LISTEN_ADDRESS=0.0.0.0:7443
export YA_ROUTER_CONTROL_TLS_CERT=/etc/ya-router/control-server.pem
export YA_ROUTER_CONTROL_TLS_KEY=/etc/ya-router/control-server-key.pem
export YA_ROUTER_CONTROL_CLIENT_CA=/etc/ya-router/control-client-ca.pem
export YA_ROUTER_CONTROL_ADMIN_SUBJECTS='spiffe://example/ya-admin'
export YA_ROUTER_CONTROL_OPERATOR_SUBJECTS='spiffe://example/ya-operator'
export YA_ROUTER_CONTROL_VIEWER_SUBJECTS='spiffe://example/ya-viewer'
```

Mappings are comma-separated and can match a URI SAN, the full X.509 subject,
or the Common Name. URI SAN values are preferred. Non-loopback plaintext
startup is rejected.

## Authorization

Roles are ordered:

| Role | Foundation permission |
|---|---|
| `viewer` | Read `/control/v1/meta` and future redacted resources |
| `operator` | Viewer permissions plus future operational commands |
| `admin` | Operator permissions plus future configuration and rollback commands |

Authentication failures return `401`. An authenticated identity with an
insufficient role returns `403`. Both outcomes produce redacted audit events.
Audit events include actor, role, method, path, result, status, request ID, and
timestamp; they exclude headers, bodies, query strings, credentials, device
codes, and raw account identifiers.

## Version negotiation

Clients send `X-YA-Client-Version`. The current foundation accepts the current
client line (`1.1.x`) and previous line (`1.0.x`). `/control/v1/meta` remains
available to unsupported clients and returns `compatible=false`; other control
resources reject unsupported versions with `426` before executing work.

All responses include `X-Request-ID`. Errors use a stable envelope:

```json
{
  "error": {
    "code": "control_forbidden",
    "message": "The authenticated identity does not have the required role.",
    "retryable": false,
    "request_id": "req_...",
    "details": {
      "required_role": "admin"
    }
  }
}
```

## Idempotency framework

Future mutation routes are registered as idempotent and require an
`Idempotency-Key` header. The service serializes concurrent duplicates,
replays the first response for an identical payload, and returns `409` if the
same key is reused with a different payload. Request bodies are bounded and a
panic is converted into a typed, replayable `500` response rather than leaving
waiters blocked.

## Contract

The machine-readable contract is
[`docs/api/control-v1.openapi.yaml`](api/control-v1.openapi.yaml).
