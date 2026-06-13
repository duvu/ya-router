## Context

The service persists auth state by writing `config.json.tmp` and renaming it to `config.json` in the runtime config directory. In Docker, that directory is a bind mount from the host into `/home/appuser/.local/share/github-copilot-svcs`. The image runs as non-root `appuser`, but the mounted host path often has ownership and mode that do not allow UID 10001 to create temporary files.

This makes `auth copilot` and `auth codex` fail at the first persistence step, even though the service process is otherwise healthy. The fix must therefore occur at container startup and must be safe for existing bind mounts.

## Goals / Non-Goals

**Goals:**
- Ensure the mounted config directory is writable by the runtime user before the service starts.
- Preserve the current runtime behavior for non-Docker and existing local users.
- Keep the fix limited to the Docker runtime path so auth persistence works on standard bind-mounted config directories.

**Non-Goals:**
- Changing the config file format or auth schema.
- Rewriting the auth logic itself.
- Requiring operators to run manual `chown` or `chmod` commands on the host.

## Decisions

### 1. Fix ownership in the image entrypoint, not in the Go code

**Decision:** Add an `entrypoint.sh` that runs as root at startup, repairs ownership and permissions on the mounted config directory, and then switches to `appuser` with `su-exec`.

**Rationale:** The failure is caused by the runtime mount permissions, not by the auth write logic itself. Fixing the mount at process startup is the smallest change that makes Docker auth writes work without changing the runtime config path or user model.

### 2. Use `chown -R appuser:appuser` plus `chmod 0775` on the mounted config directory

**Decision:** The entrypoint normalizes the mounted config directory to be writable by the container user while keeping the directory accessible for the existing bind mount.

**Rationale:** This addresses the observed `permission denied` condition for `config.json.tmp` and preserves the current bind mount layout used by the service.

## Risks / Trade-offs

- **Host directory permissions may be restrictive.** If the host mount path is readonly or owned by a different system user with no permission to change ownership, the entrypoint will still fail.
  → Mitigation: keep the fix limited to standard writable bind mounts and document the requirement for a writable host path.

- **Root startup step adds a small startup cost.** The ownership repair is trivial and only runs on container startup.
  → Mitigation: keep the repair scoped to the config directory only.

## Migration Plan

1. Build the updated image with the new entrypoint and `su-exec`.
2. Recreate the container using the existing bind mount.
3. Re-run `auth copilot` and `auth codex` inside the container and confirm config writes succeed.

Rollback: revert `Dockerfile` and `entrypoint.sh`, then redeploy the previous image.

## Open Questions

- None at this time; the fix path is limited to the container runtime setup.
