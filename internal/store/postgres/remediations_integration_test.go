//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
		if err := r.ClaimOperation(
			ctx,
			"op-1",
			func(context.Context, store.ClaimedOperation) error { return nil },
		); !errors.Is(
			err,
			store.ErrOperationHeld,
		) {
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

	if err := r.ClaimOperation(
		t.Context(),
		"missing",
		func(context.Context, store.ClaimedOperation) error { return nil },
	); !errors.Is(
		err,
		store.ErrRemediationNotFound,
	) {
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
	if err != nil || got.ID != row.ID || got.AcknowledgedHeadSHA != "h1" || got.IntentRevision != "i2" || got.RemediatedGeneration != 1 ||
		got.Hold != "" {
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

	if orgs, err := f.r.ExpireReservations(ctx, time.Now()); err != nil || len(orgs) != 1 || orgs[0] != "acme" {
		t.Fatalf("ExpireReservations = %v, %v; want one in acme", orgs, err)
	}

	if due, _ := f.r.DueRemediations(ctx, 10); len(due) != 1 {
		t.Errorf("DueRemediations after expiry = %+v, want codeowners again", due)
	}
}

// TestRemediationStore_CrossWriterDenied is IMPL-0030 1.11, run as
// rg_remediator: the stated transactions pass through RemediationStore
// (the tests above all run as the remediator), and every cross-writer
// operation fails. Deleting an open row is not an error under RLS — the
// remediations_expire policy hides it — so that case checks the row
// count, and ExpireReservations frees only the reservation.
func TestRemediationStore_CrossWriterDenied(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	row, err := reserve(t, f.r, f.repoID, "codeowners", "op-1")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.r.AppendStep(
		ctx,
		&store.RemediationStep{RemediationID: row.ID, OperationKey: "op-1", Kind: "committed", Status: "done"},
	); err != nil {
		t.Fatal(err)
	}

	run(t, f.dsns.Owner, `UPDATE remediations SET state = 'open', reserved_until = now() - interval '1 hour'`)

	for _, sql := range []string{
		`UPDATE control_results SET eval_generation = 9`,
		`UPDATE remediations SET observed_head_sha = 'h9'`,
		`UPDATE remediations SET closed_generation = 9`,
		`UPDATE remediations SET next_eligible_at = now()`,
		`UPDATE remediation_steps SET status = 'failed'`,
		`DELETE FROM remediation_steps`,
		`DELETE FROM remediation_events`,
	} {
		if err := exec(t, f.dsns.Remediator, sql); err == nil {
			t.Errorf("%s as rg_remediator: allowed, want permission denied", sql)
		}
	}

	conn, err := pgx.Connect(ctx, f.dsns.Remediator)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	tag, err := conn.Exec(ctx, `DELETE FROM remediations WHERE state = 'open'`)
	if err != nil || tag.RowsAffected() != 0 {
		t.Errorf("DELETE an open row as rg_remediator = %v rows, %v; want 0 rows", tag.RowsAffected(), err)
	}

	if orgs, err := f.r.ExpireReservations(ctx, time.Now()); err != nil || len(orgs) != 0 {
		t.Errorf("ExpireReservations over an open row = %v, %v; want none", orgs, err)
	}
}

// TestRemediationStore_ConcurrentReservations is IMPL-0030 Phase 1's
// success criterion: sixteen concurrent ReserveSlot calls for sixteen
// controls on one repository at max_open_prs = 3 leave exactly three
// reserved rows, and every other call gets ErrPRCap.
func TestRemediationStore_ConcurrentReservations(t *testing.T) {
	r, repoID := remediationStores(t)

	const n = 16

	errs := make(chan error, n)

	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, err := r.ReserveSlot(context.Background(), &store.SlotRequest{
				RepositoryID: repoID, ControlID: fmt.Sprintf("c%02d", i), Epoch: 1, OperationKey: fmt.Sprintf("op-%02d", i),
				Intent: "i1", Generation: 1, TTL: time.Hour,
			})
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	var reserved, capped int
	for err := range errs {
		switch {
		case err == nil:
			reserved++
		case errors.Is(err, store.ErrPRCap):
			capped++
		default:
			t.Errorf("ReserveSlot: %v", err)
		}
	}

	if reserved != 3 || capped != n-3 {
		t.Errorf("concurrent ReserveSlot = %d reserved, %d capped; want 3 and %d", reserved, capped, n-3)
	}
}

// TestControlsStore_ObservePullRequests is IMPL-0030 2.2, run as
// rg_evaluator: an open PR's head is observed and evented only when it
// moves; a user close sets closed_by_user with closed_generation and
// next_eligible_at = closed_at + reopen_after; a row no longer open is
// never reclassified.
func TestControlsStore_ObservePullRequests(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	row, err := reserve(t, f.r, f.repoID, "codeowners", "op-1")
	if err != nil {
		t.Fatal(err)
	}

	run(t, f.dsns.Owner, `UPDATE remediations SET state = 'open', pr_number = 7`)

	tracked, err := f.eval.TrackedPullRequests(ctx, f.repoID)
	if err != nil || len(tracked) != 1 || tracked[0].PRNumber != 7 || tracked[0].RemediationID != row.ID {
		t.Fatalf("TrackedPullRequests = %+v, %v; want #7", tracked, err)
	}

	events := func() int {
		var n int
		queryArgs(t, f.dsns.Owner, `SELECT count(*)::int FROM remediation_events WHERE kind = 'observed'`, &n)

		return n
	}

	open := store.PRObservation{RemediationID: row.ID, PRNumber: 7, State: store.PRObservedOpen, HeadSHA: "h1"}
	for range 2 {
		if err := f.eval.ObservePullRequests(ctx, []store.PRObservation{open}); err != nil {
			t.Fatalf("ObservePullRequests(open): %v", err)
		}
	}

	if got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners"); got.ObservedHeadSHA != "h1" || got.ObservedAt == nil || events() != 1 {
		t.Errorf("after two opens: head %q, observed_at %v, %d events; want h1, set, 1", got.ObservedHeadSHA, got.ObservedAt, events())
	}

	closedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := f.eval.ObservePullRequests(ctx, []store.PRObservation{{
		RemediationID: row.ID, PRNumber: 7, State: store.PRObservedClosed, ClosedAt: closedAt,
	}}); err != nil {
		t.Fatalf("ObservePullRequests(closed): %v", err)
	}

	got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners")
	if got.State != store.StateClosedByUser || got.ClosedGeneration == nil || *got.ClosedGeneration != 1 ||
		got.NextEligibleAt == nil || !got.NextEligibleAt.Equal(closedAt.Add(336*time.Hour)) {
		t.Fatalf("after close = %+v; want closed_by_user, closed_generation 1, next_eligible_at closed_at + 14d", got)
	}

	// A merge seen after the close does not reclassify the terminal row.
	if err := f.eval.ObservePullRequests(ctx, []store.PRObservation{{
		RemediationID: row.ID, PRNumber: 7, State: store.PRObservedMerged, ClosedAt: closedAt,
	}}); err != nil {
		t.Fatal(err)
	}

	if got, _ := f.r.LatestRemediation(ctx, f.repoID, "codeowners"); got.State != store.StateClosedByUser || events() != 2 {
		t.Errorf("merge after close: state %q, %d events; want closed_by_user, 2", got.State, events())
	}

	// rg_evaluator holds none of the remediator's columns.
	for _, sql := range []string{
		`UPDATE remediations SET acknowledged_head_sha = 'x'`,
		`UPDATE remediations SET remediated_generation = 9`,
		`UPDATE remediations SET intent_revision = 'x'`,
		`UPDATE remediations SET hold = 'permission'`,
		`UPDATE control_results SET remediated_generation = 9`,
		`INSERT INTO remediation_steps (remediation_id, operation_key, seq, kind, status, occurred_at) VALUES (1, 'x', 9, 'committed', 'done', now())`,
	} {
		if err := exec(t, f.dsns.Evaluator, sql); err == nil {
			t.Errorf("%s as rg_evaluator: allowed, want permission denied", sql)
		}
	}
}

// TestRemediationStore_RemediationTarget is what IMPL-0030 3.1 and 3.5
// read: the assignment, the Remediation App's reach and the result, and
// nil for a control that is not assigned.
func TestRemediationStore_RemediationTarget(t *testing.T) {
	r, repoID := remediationStores(t)

	got, err := r.RemediationTarget(t.Context(), repoID, "codeowners")
	if err != nil || got == nil {
		t.Fatalf("RemediationTarget = %v, %v; want the assignment", got, err)
	}

	if !got.Active || got.Epoch == 0 || got.State != "active" || got.Revision != "r1" || got.HasResult {
		t.Errorf("RemediationTarget = %+v, want an active, unevaluated assignment", got)
	}

	if missing, err := r.RemediationTarget(t.Context(), repoID, "labels"); err != nil || missing != nil {
		t.Errorf("RemediationTarget(unassigned) = %+v, %v; want nil", missing, err)
	}
}

// TestRemediationStore_PullRequestPathReads is what IMPL-0030 Phase 4
// adds: a reservation carries the run's intent, the target reports the
// due predicate, a run with no remediation row acknowledges the
// generation on the result alone, and the record stamps
// last_remediation_at.
func TestRemediationStore_PullRequestPathReads(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	run(t, f.dsns.Owner, `UPDATE control_assignments SET effective_mode = 'remediate', requested_mode = 'remediate'`)
	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	got, err := f.r.RemediationTarget(ctx, f.repoID, "codeowners")
	if err != nil || got == nil || !got.Due || got.ControlVersion != 1 || got.Generation != 1 {
		t.Fatalf("RemediationTarget = %+v, %v; want due at generation 1, version 1", got, err)
	}

	// Nothing opened: the generation is acknowledged and the row is no
	// longer due.
	ok, err := f.r.RecordRun(ctx, &store.RunRecord{Org: "acme", RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, Generation: 1})
	if err != nil || !ok {
		t.Fatalf("RecordRun without a row = %v, %v; want recorded", ok, err)
	}

	if got, _ := f.r.RemediationTarget(ctx, f.repoID, "codeowners"); got.Due {
		t.Error("still due after acknowledging the generation")
	}

	row, err := f.r.ReserveSlot(ctx, &store.SlotRequest{
		RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "op-1", Intent: "i1", Generation: 1, TTL: time.Hour,
		BaseSHA: "b1", Branch: "repo-guardian/codeowners", ExpectedHead: "b1",
	})
	if err != nil || row.BaseSHA != "b1" || row.Branch != "repo-guardian/codeowners" || row.ExpectedHead != "b1" {
		t.Fatalf("ReserveSlot = %+v, %v; want the intent on the reservation", row, err)
	}

	if err := f.r.TouchLastRemediation(ctx, f.repoID); err != nil {
		t.Fatalf("TouchLastRemediation = %v", err)
	}

	var stamped bool
	queryArgs(t, f.dsns.Owner, `SELECT last_remediation_at IS NOT NULL FROM repository_policy_state WHERE repository_id = $1`, &stamped, f.repoID)

	if !stamped {
		t.Error("last_remediation_at not stamped")
	}
}

// TestRemediationStore_APIRemediation is IMPL-0030 5.1-5.3's store side:
// an API row holds the one-open slot but never the PR cap, a retried key
// returns its row, and a superseded recommendation releases the slot for
// the direct apply that follows.
func TestRemediationStore_APIRemediation(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	// A cap of zero: the PR path could reserve nothing.
	run(t, f.dsns.Owner, `UPDATE repository_policy_state SET max_open_prs = 0`)

	rec := &store.APIRemediation{
		RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "api-1", State: store.StateRecommended,
		Intent: "i1", Generation: 1, Proposal: []byte(`[{"key":"setting:has_wiki","before":true,"after":false}]`),
	}

	row, err := f.r.InsertAPIRemediation(ctx, rec)
	if err != nil || row.Kind != store.RemediationAPI || row.State != store.StateRecommended || len(row.Proposal) == 0 {
		t.Fatalf("InsertAPIRemediation = %+v, %v; want a recommended api row with its proposal", row, err)
	}

	if again, err := f.r.InsertAPIRemediation(ctx, rec); err != nil || again.ID != row.ID {
		t.Fatalf("retried InsertAPIRemediation = %+v, %v; want row %d", again, err, row.ID)
	}

	direct := &store.APIRemediation{
		RepositoryID: f.repoID, ControlID: "codeowners", Epoch: epoch, OperationKey: "api-2", State: store.StateFailed, Intent: "i2", Generation: 1,
	}

	if _, err := f.r.InsertAPIRemediation(ctx, direct); err == nil {
		t.Fatal("a second open api row was inserted beside the recommendation")
	}

	err = f.r.ClaimOperation(ctx, "api-1", func(ctx context.Context, op store.ClaimedOperation) error {
		r := op.Remediation()
		r.State = store.StateSuperseded

		return op.Update(ctx, &r)
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, err := f.r.InsertAPIRemediation(ctx, direct); err != nil || got.State != store.StateFailed {
		t.Fatalf("direct after supersede = %+v, %v; want a failed (resumable) row", got, err)
	}
}

// TestRemediationStore_RemediateDiscovery is IMPL-0030 6.4's store side,
// as rg_remediator: the Remediation App's discovery writes its own
// installation and access rows and reports the repositories whose access
// changed, and pruning a vanished installation reports its repositories.
func TestRemediationStore_RemediateDiscovery(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()
	inst := &store.AppInstallation{InstallationID: 77, AccountLogin: "acme", AccountType: "organization", RepositorySelection: "selected"}

	changed, err := f.r.RefreshRemediateAccess(ctx, inst, []int64{101, 999})
	if err != nil || len(changed) != 1 || changed[0].ProviderRepoID != 101 || changed[0].Name != "api" {
		t.Fatalf("first refresh = %+v, %v; want acme/api gained access (999 is unknown)", changed, err)
	}

	if got, _ := f.r.RemediationTarget(ctx, f.repoID, "codeowners"); got == nil || !got.Remediable || got.InstallationID != 77 {
		t.Fatalf("RemediationTarget = %+v; want remediable through installation 77", got)
	}

	if changed, err := f.r.RefreshRemediateAccess(ctx, inst, []int64{101}); err != nil || len(changed) != 0 {
		t.Errorf("unchanged refresh = %+v, %v; want nothing changed", changed, err)
	}

	if changed, err := f.r.PruneRemediateInstallations(ctx, nil); err != nil || len(changed) != 1 || changed[0].ProviderRepoID != 101 {
		t.Fatalf("prune = %+v, %v; want acme/api lost access", changed, err)
	}

	if got, _ := f.r.RemediationTarget(ctx, f.repoID, "codeowners"); got.Remediable {
		t.Error("still remediable after its installation was pruned")
	}

	if err := f.r.RecordServiceRun(ctx, &store.ServiceRun{
		Kind: store.ServiceRunRemediationDiscovery, Success: true, StartedAt: time.Now(), FinishedAt: time.Now(),
	}); err != nil {
		t.Errorf("RecordServiceRun as rg_remediator = %v", err)
	}
}

// TestRemediationStore_MaintenanceTarget reads a candidate with the
// repository, reach and the observed time of its cause.
func TestRemediationStore_MaintenanceTarget(t *testing.T) {
	f := newRemediationFixture(t)
	ctx := t.Context()

	row, err := reserve(t, f.r, f.repoID, "codeowners", "op-mt")
	if err != nil {
		t.Fatal(err)
	}

	got, err := f.r.MaintenanceTarget(ctx, row.ID)
	if err != nil || got.Org != "acme" || got.Name != "api" || got.OperationKey != "op-mt" || got.ObservedAt.IsZero() {
		t.Fatalf("MaintenanceTarget = %+v, %v", got, err)
	}

	if _, err := f.r.MaintenanceTarget(ctx, row.ID+1000); !errors.Is(err, store.ErrRemediationNotFound) {
		t.Errorf("MaintenanceTarget of a missing row = %v, want ErrRemediationNotFound", err)
	}
}
