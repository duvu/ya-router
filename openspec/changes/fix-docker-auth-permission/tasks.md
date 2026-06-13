## 1. Runtime Fix

- [ ] 1.1 Add an entrypoint script that repairs the mounted config directory before switching to the runtime user.
- [ ] 1.2 Update the runtime image to install `su-exec` and invoke the entrypoint.

## 2. Verification

- [ ] 2.1 Build the updated image locally.
- [ ] 2.2 Run the container with the existing bind-mounted config directory.
- [ ] 2.3 Verify `auth copilot` and `auth codex` can persist config without `permission denied`.
