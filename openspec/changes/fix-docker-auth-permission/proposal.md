## Why

Docker auth flows currently fail because the container runs as non-root `appuser`, but the mounted config directory on the host is owned by the host user and often lacks write access for UID 10001. The first write to `config.json.tmp` therefore fails with `permission denied`, even though the service itself is healthy.

## What Changes

- Add a runtime entrypoint that repairs the mounted config directory before dropping to `appuser`.
- Ensure the container image installs the helper needed to switch users safely.
- Document that the fix restores `auth copilot` and `auth codex` persistence inside Docker.

## Capabilities

### New Capabilities
- `docker-auth-permissions`: the runtime MUST ensure the mounted config directory is writable by the service user before auth writes occur.

### Modified Capabilities

None.

## Impact

- Affected code: `Dockerfile`, `entrypoint.sh`, and the Docker runtime path used by `auth copilot` / `auth codex`.
- Affected behavior: auth persistence inside Docker will succeed on existing bind mounts without manual `chown` on the host.
