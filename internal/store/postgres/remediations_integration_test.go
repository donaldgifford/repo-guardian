//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// remediationStores returns the evaluator store (for discovery) and a
// RemediationStore connected as rg_remediator, plus a discovered
// repository whose cap is three open pull requests.
func remediationStores(t *testing.T) (*postgres.RemediationStore, int64) {
	t.Helper()

	s, dsns := evaluatorStore(t)
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	pool, err := pgxpool.New(t.Context(), dsns.Remediator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return postgres.NewRemediationStore(pool), repo.ID
}

func reserve(t *testing.T, r *postgres.RemediationStore, repoID int64, control, key string) (*store.Remediation, error) {
	t.Helper()

	return r.ReserveSlot(t.Context(), &store.SlotRequest{
		RepositoryID: repoID, ControlID: control, Epoch: 1, OperationKey: key, Intent: "i1", Generation: 1, TTL: time.Hour,
	})
}

// TestRemediationStore_ReserveSlot is IMPL-0030 1.5: a reservation is
// idempotent on its operation key and the fourth control is refused at
// the cap of three.
func TestRemediationStore_ReserveSlot(t *testing.T) {
	r, repoID := remediationStores(t)

	first, err := reserve(t, r, repoID, "codeowners", "op-1")
	if err != nil {
		t.Fatalf("ReserveSlot: %v", err)
	}

	if first.State != store.StateReserved || first.ReservedUntil == nil {
		t.Fatalf("ReserveSlot = %+v, want reserved with reserved_until", first)
	}

	again, err := reserve(t, r, repoID, "codeowners", "op-1")
	if err != nil || again.ID != first.ID {
		t.Fatalf("ReserveSlot retry = %v, %v; want row %d", again, err, first.ID)
	}

	for i, c := range []string{"dependabot", "catalog-info"} {
		if _, err := reserve(t, r, repoID, c, "op-"+c); err != nil {
			t.Fatalf("ReserveSlot #%d: %v", i+2, err)
		}
	}

	if _, err := reserve(t, r, repoID, "labels", "op-labels"); !errors.Is(err, store.ErrPRCap) {
		t.Fatalf("ReserveSlot past the cap = %v, want ErrPRCap", err)
	}

	got, err := r.LatestRemediation(t.Context(), repoID, "codeowners")
	if err != nil || got == nil || got.ID != first.ID {
		t.Fatalf("LatestRemediation = %v, %v; want row %d", got, err, first.ID)
	}
}

// TestRemediationStore_ClaimOperation is IMPL-0030 1.5: a second claim
// of a held operation gets ErrOperationHeld, the journal commits from
// outside the claim, and updates write through it.
func TestRemediationStore_ClaimOperation(t *testing.T) {
	r, repoID := remediationStores(t)

	row, err := reserve(t, r, repoID, "codeowners", "op-1")
	if err != nil {
		t.Fatal(err)
	}

	err = r.ClaimOperation(t.Context(), "op-1", func(ctx context.Context, op store.ClaimedOperation) error {
		if err := r.ClaimOperation(ctx, "op-1", func(context.Context, store.ClaimedOperation) error { return nil }); !errors.Is(err, store.ErrOperationHeld) {
			t.Errorf("nested ClaimOperation = %v, want ErrOperationHeld", err)
		}

		if _, err := r.AppendStep(ctx, &store.RemediationStep{
			RemediationID: row.ID, OperationKey: "op-1", Kind: "committed", Status: "done", CommittedSHA: "abc",
		}); err != nil {
			return err
		}

		if err := r.AppendEvent(ctx, row.ID, "opened", nil); err != nil {
			return err
		}

		next := op.Remediation()
		next.State, next.PRNumber, next.PRURL, next.Branch = store.StateOpen, 7, "https://example/pull/7", "repo-guardian/codeowners"
		next.ReservedUntil = nil

		return op.Update(ctx, &next)
	})
	if err != nil {
		t.Fatalf("ClaimOperation: %v", err)
	}

	if err := r.ClaimOperation(t.Context(), "missing", func(context.Context, store.ClaimedOperation) error { return nil }); !errors.Is(err, store.ErrRemediationNotFound) {
		t.Fatalf("ClaimOperation(missing) = %v, want ErrRemediationNotFound", err)
	}

	steps, err := r.Steps(t.Context(), row.ID)
	if err != nil || len(steps) != 1 || steps[0].Seq != 1 || steps[0].CommittedSHA != "abc" {
		t.Fatalf("Steps = %+v, %v; want one commit step at seq 1", steps, err)
	}

	got, err := r.LatestRemediation(t.Context(), repoID, "codeowners")
	if err != nil || got.State != store.StateOpen || got.PRNumber != 7 {
		t.Fatalf("LatestRemediation = %+v, %v; want open #7", got, err)
	}
}
