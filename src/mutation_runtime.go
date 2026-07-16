// mutation_runtime.go orchestrates revision-safe management mutations
// (YA-TUI-08). A mutation reads the current revision, applies a typed change to
// a validated config clone, and either previews (dry-run/diff) or commits it
// through the daemon-owned state manager using compare-and-swap. A committed
// change is hot-reloaded by reconciling the provider runtime — never by
// invoking systemd, Docker, a shell, or a process self-restart.
package yarouter

import (
	"context"
	"errors"
	"fmt"
	"time"

	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	statepkg "github.com/duvu/ya-router/internal/state"
)

// MutationResult is the redacted outcome of a mutation preview or apply.
type MutationResult struct {
	DryRun          bool     `json:"dry_run"`
	Applied         bool     `json:"applied"`
	CurrentRevision uint64   `json:"current_revision"`
	NextRevision    uint64   `json:"next_revision"`
	Changed         bool     `json:"changed"`
	ChangedPaths    []string `json:"changed_paths,omitempty"`
	RestartRequired []string `json:"restart_required,omitempty"`
}

// mutationReloader hot-reloads the provider runtime after a committed config
// change. It is satisfied by the provider Manager.
type mutationReloader interface {
	Reconcile(ctx context.Context, desired []providerpkg.DesiredProvider) (providerpkg.DrainReport, error)
}

// applyManagedMutation runs one revision-safe mutation against the daemon-owned
// state. On a non-dry-run success it reconciles the provider runtime so the
// change takes effect without a restart. A rejected validation or a revision
// conflict leaves the effective runtime untouched.
func applyManagedMutation(request controlpkg.MutationRequest, actor string, reloader mutationReloader) (MutationResult, error) {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	manager := currentConfigState()
	if manager == nil {
		return MutationResult{}, fmt.Errorf("managed configuration state is not active")
	}
	snapshot := manager.Snapshot()

	// Compare-and-swap: the caller must target the current revision.
	if request.ExpectedRevision != snapshot.Revision {
		return MutationResult{}, &statepkg.ConflictError{Expected: request.ExpectedRevision, Current: snapshot.Revision}
	}

	next, err := controlpkg.ApplyMutation(snapshot.Desired, request)
	if err != nil {
		return MutationResult{}, err
	}

	// Validate + preview through the state manager (also enforces the config
	// validator, including routing/virtual-model invariants).
	preview, err := manager.Validate(next, next)
	if err != nil {
		return MutationResult{}, err
	}

	result := MutationResult{
		DryRun:          request.DryRun,
		CurrentRevision: preview.CurrentRevision,
		NextRevision:    preview.NextRevision,
		Changed:         preview.Changed,
		ChangedPaths:    preview.ChangedPaths,
		RestartRequired: preview.RestartRequired,
	}
	if request.DryRun {
		return result, nil
	}

	applied, err := manager.Apply(snapshot.Revision, next, next, actor, false)
	if err != nil {
		return MutationResult{}, err
	}
	result.Applied = true
	result.NextRevision = applied.Revision

	// Hot reload: reconcile providers to the new effective configuration. A
	// reconcile failure is reported but the config revision is already durably
	// committed; the next request observes the new snapshot.
	if reloader != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, reloadErr := reloader.Reconcile(ctx, desiredProviders(applied.Effective)); reloadErr != nil {
			return result, fmt.Errorf("configuration committed at revision %d but provider reload failed: %w", applied.Revision, reloadErr)
		}
	}
	return result, nil
}

// mutationHTTPStatus maps a mutation error to a control HTTP status so the API
// returns typed, stable responses.
func mutationHTTPStatus(err error) (int, string) {
	var conflict *statepkg.ConflictError
	if errors.As(err, &conflict) {
		return 409, "revision_conflict"
	}
	return 400, "invalid_mutation"
}

// mutationExecutor adapts applyManagedMutation to control.MutationExecutor.
type mutationExecutor struct {
	reloader mutationReloader
}

func (executor mutationExecutor) Execute(request controlpkg.MutationRequest, actor string) (any, int, string, error) {
	result, err := applyManagedMutation(request, actor, executor.reloader)
	if err != nil {
		status, code := mutationHTTPStatus(err)
		return nil, status, code, err
	}
	return result, 200, "", nil
}
