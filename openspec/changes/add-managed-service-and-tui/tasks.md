# Tasks

## Planning baseline

- [x] Document the service/client boundary and naming decision.
- [x] Define control/data listener separation and management trust model.
- [x] Define runtime snapshot, provider lifecycle, state revision, and secret-store contracts.
- [x] Define operation/auth-session and provider/account/model resource contracts.
- [x] Define TUI, automation, systemd, Docker, release, and compatibility requirements.
- [x] Publish an ordered implementation roadmap.
- [ ] Link the GitHub epic and child issues into the roadmap and PR.

## Implementation tracking

- [ ] YA-TUI-01 refactor package and binary boundaries.
- [ ] YA-TUI-02 add runtime/provider manager.
- [ ] YA-TUI-03 add revisioned single-writer state.
- [ ] YA-TUI-04 add isolated control API/security foundation.
- [ ] YA-TUI-05 add read-only control resources.
- [ ] YA-TUI-06 add async operation/auth-session framework.
- [ ] YA-TUI-07 add provider auth adapters and secret store.
- [ ] YA-TUI-08 add revision-safe mutations and hot reload.
- [ ] YA-TUI-09 add client SDK and scriptable commands.
- [ ] YA-TUI-10 add read-only TUI.
- [ ] YA-TUI-11 add auth and mutation TUI workflows.
- [ ] YA-TUI-12 add production packaging/hardening.
- [ ] YA-TUI-13 complete production acceptance gates.

