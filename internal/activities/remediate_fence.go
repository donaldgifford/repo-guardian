package activities

import (
	"context"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// fence re-reads the assignment and returns ErrStaleEpoch when its epoch,
// state, effective mode, revision or the Remediation App's reach moved
// since the run read it (IMPL-0030 3.5). Every external write is
// immediately preceded by it; TestRemediate_EveryWriteIsFenced walks
// the call sites.
func (run *remediationRun) fence(ctx context.Context) error {
	now, err := run.store.RemediationTarget(ctx, run.in.RepositoryID, run.in.Control)
	if err != nil {
		return fmt.Errorf("fence: %w", err)
	}

	if now == nil || !fenceEqual(run.target, now) {
		return ErrStaleEpoch
	}

	return nil
}

// fenceEqual compares what the fence guards.
func fenceEqual(a, b *store.RemediationTarget) bool {
	return a.Active == b.Active && a.Epoch == b.Epoch && a.State == b.State &&
		a.EffectiveMode == b.EffectiveMode && a.Revision == b.Revision && a.Remediable == b.Remediable
}
