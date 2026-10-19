//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// remediationFixture is a discovered repository (cap three open pull
// requests, codeowners assigned) with both application stores.
type remediationFixture struct {
	eval   *postgres.ControlsStore
	r      *postgres.RemediationStore
	dsns   pgtest.ControlsDSNs
	repoID int64
}

func newRemediationFixture(t *testing.T) *remediationFixture {
	t.Helper()

	s, dsns := evaluatorStore(t)
	repo := discover(t, s, &fakeResolver{assignments: []policy.Assignment{codeowners("r1")}})

	pool, err := pgxpool.New(t.Context(), dsns.Remediator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return &remediationFixture{eval: s, r: postgres.NewRemediationStore(pool), dsns: dsns, repoID: repo.ID}
}

// remediationStores is newRemediationFixture for tests that need only
// the remediator.
func remediationStores(t *testing.T) (*postgres.RemediationStore, int64) {
	t.Helper()

	f := newRemediationFixture(t)

	return f.r, f.repoID
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

// TestRemediationStore_RecordRun is IMPL-0030 1.6: the record writes
// remediated_generation on both rows, acknowledges the processed head
// only while it is still the observed one, clears hold, is idempotent,
// and discards a run whose epoch moved.
func TestRemediationStore_RecordRun(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	row, err := f.r.ReserveSlot(ctx, &store.SlotRequest{
		RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "op-1", Intent: "i1", Generation: 1, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	run(t, f.dsns.Owner, `UPDATE remediations SET observed_head_sha = 'h1', hold = 'permission'`)
	run(t, f.dsns.Owner, `UPDATE control_results SET hold = 'conflict', hold_since = now()`)

	rec := &store.RunRecord{
		Org: "acme", RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "op-1",
		Generation: 1, IntentRevision: "i2", ProcessedHead: "h1",
	}

	for range 2 {
		if ok, err := f.r.RecordRun(ctx, rec); err != nil || !ok {
			t.Fatalf("RecordRun = %v, %v; want recorded", ok, err)
		}
	}

	got, err := f.r.LatestRemediation(ctx, f.repoID, "codeowners")
	if err != nil || got.ID != row.ID || got.AcknowledgedHeadSHA != "h1" || got.IntentRevision != "i2" || got.RemediatedGeneration != 1 || got.Hold != "" {
		t.Fatalf("after RecordRun = %+v, %v; want acknowledged h1, intent i2, generation 1, no hold", got, err)
	}

	var hold *string
	queryArgs(t, f.dsns.Owner, `SELECT hold FROM control_results WHERE repository_id = $1`, &hold, f.repoID)

	if hold != nil {
		t.Errorf("control_results.hold = %q, want NULL", *hold)
	}

	// A head observed after the run started stays unacknowledged.
	run(t, f.dsns.Owner, `UPDATE remediations SET observed_head_sha = 'h2'`)

	if ok, err := f.r.RecordRun(ctx, rec); err != nil || !ok {
		t.Fatalf("RecordRun = %v, %v", ok, err)
	}

	if got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners"); got.AcknowledgedHeadSHA != "h1" {
		t.Errorf("acknowledged_head_sha = %q after a newer observed head, want h1", got.AcknowledgedHeadSHA)
	}

	discarded := metrics.ResultsDiscardedTotal.WithLabelValues("acme", "stale_epoch")
	before := testutil.ToFloat64(discarded)

	stale := *rec
	stale.Epoch, stale.Generation = epoch+100, 9

	if ok, err := f.r.RecordRun(ctx, &stale); err != nil || ok {
		t.Fatalf("RecordRun(stale epoch) = %v, %v; want discarded", ok, err)
	}

	if d := testutil.ToFloat64(discarded) - before; d != 1 {
		t.Errorf("results_discarded_total{stale_epoch} moved %v, want 1", d)
	}

	if got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners"); got.RemediatedGeneration != 1 {
		t.Errorf("remediated_generation = %d after a discarded run, want 1", got.RemediatedGeneration)
	}
}

// TestRemediationStore_SetHold is IMPL-0030 1.7: a hold lands on the
// result under its epoch and keeps hold_since while unchanged, a stale
// epoch writes nothing, and once the result is gone only a permission
// hold lands, on the newest remediation.
func TestRemediationStore_SetHold(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	set := func(e int64, hold string) bool {
		t.Helper()

		ok, err := f.r.SetHold(ctx, f.repoID, "codeowners", e, hold)
		if err != nil {
			t.Fatalf("SetHold(%d, %q): %v", e, hold, err)
		}

		return ok
	}

	since := func() *time.Time {
		var ts *time.Time
		queryArgs(t, f.dsns.Owner, `SELECT hold_since FROM control_results WHERE repository_id = $1`, &ts, f.repoID)

		return ts
	}

	if !set(epoch, store.HoldPRCap) || since() == nil {
		t.Fatal("SetHold(pr_cap) did not land on the result")
	}

	first := since()
	if !set(epoch, store.HoldPRCap) || !since().Equal(*first) {
		t.Errorf("repeated hold moved hold_since from %v to %v", first, since())
	}

	if set(epoch+100, store.HoldConflict) {
		t.Error("SetHold(stale epoch) = true, want false")
	}

	if !set(epoch, "") || since() != nil {
		t.Error("SetHold(\"\") did not clear the hold")
	}

	if _, err := reserve(t, f.r, f.repoID, "codeowners", "op-1"); err != nil {
		t.Fatal(err)
	}

	run(t, f.dsns.Owner, `DELETE FROM control_results`)

	if set(epoch, store.HoldConflict) {
		t.Error("SetHold(conflict) with no result = true, want false")
	}

	if !set(epoch, store.HoldPermission) {
		t.Fatal("SetHold(permission) with no result = false, want the remediation held")
	}

	if got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners"); got.Hold != store.HoldPermission {
		t.Errorf("remediations.hold = %q, want permission", got.Hold)
	}
}

// TestRemediationStore_DueAndMaintenance is IMPL-0030 1.8: a failing,
// remediable result is due; a reservation takes it out of due; once past
// reserved_until it is an expire candidate and ExpireReservations frees
// it, after which it is due again. The full truth table is task 1.10.
func TestRemediationStore_DueAndMaintenance(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	due, err := f.r.DueRemediations(ctx, 10)
	if err != nil || len(due) != 1 || due[0].RepositoryID != f.repoID || due[0].ControlID != "codeowners" {
		t.Fatalf("DueRemediations = %+v, %v; want codeowners", due, err)
	}

	if _, err := f.r.ReserveSlot(ctx, &store.SlotRequest{
		RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "op-1", Intent: "i1", Generation: 1, TTL: -time.Minute,
	}); err != nil {
		t.Fatal(err)
	}

	if due, _ := f.r.DueRemediations(ctx, 10); len(due) != 0 {
		t.Errorf("DueRemediations with a reservation = %+v, want none", due)
	}

	m, err := f.r.MaintenanceCandidates(ctx, 10)
	if err != nil || len(m) != 1 || m[0].Action != store.MaintenanceExpire || m[0].State != store.StateReserved {
		t.Fatalf("MaintenanceCandidates = %+v, %v; want one expire", m, err)
	}

	if n, err := f.r.ExpireReservations(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("ExpireReservations = %d, %v; want 1", n, err)
	}

	if due, _ := f.r.DueRemediations(ctx, 10); len(due) != 1 {
		t.Errorf("DueRemediations after expiry = %+v, want codeowners again", due)
	}
}
