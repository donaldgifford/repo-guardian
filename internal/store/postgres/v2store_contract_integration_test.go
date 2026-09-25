//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// The nil-vs-empty rule (IMPL-0023, INV-0015) made explicit: learning
// nothing never touches findings; knowing no rule applies clears them.

func (f *v2Fixture) findingCount(t *testing.T) int {
	t.Helper()

	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM findings WHERE repository_id = $1`, f.repoID).Scan(&n); err != nil {
		t.Fatalf("count findings: %v", err)
	}

	return n
}

func TestRecordCheckError_NeverTouchesFindings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"), compliant("renovate"))

	if err := f.store.RecordCheckError(ctx, &store.CheckErrorRecord{
		Key: "k2", RepositoryID: f.repoID, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0.Add(time.Hour), Err: "boom",
	}); err != nil {
		t.Fatalf("RecordCheckError: %v", err)
	}

	if n := f.findingCount(t); n != 2 {
		t.Errorf("findings = %d, want 2 untouched", n)
	}

	if n := f.eventCount(t); n != 2 {
		t.Errorf("events = %d, want 2", n)
	}

	repo, err := f.store.GetRepository(ctx, f.repoID)
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	if repo.LastCheckOutcome != "error" || repo.LastError != "boom" {
		t.Errorf("repository = %q/%q, want error/boom", repo.LastCheckOutcome, repo.LastError)
	}
}

func TestPark_AccessDeniedKeepsFindings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))

	if err := f.store.Park(ctx, f.repoID, store.ParkAccessDenied, false); err != nil {
		t.Fatalf("Park: %v", err)
	}

	if n := f.findingCount(t); n != 1 {
		t.Errorf("findings = %d, want 1 kept", n)
	}

	repo, err := f.store.GetRepository(ctx, f.repoID)
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}

	if repo.Active || repo.ParkReason == nil || *repo.ParkReason != store.ParkAccessDenied {
		t.Errorf("repository active=%v reason=%v, want parked access_denied", repo.Active, repo.ParkReason)
	}
}

func TestPark_ArchivedClearsFindingsWithEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"), compliant("renovate"))

	if err := f.store.Park(ctx, f.repoID, store.ParkArchived, true); err != nil {
		t.Fatalf("Park: %v", err)
	}

	if n := f.findingCount(t); n != 0 {
		t.Errorf("findings = %d, want 0", n)
	}

	// Two creations plus two removals.
	if n := f.eventCount(t); n != 4 {
		t.Errorf("events = %d, want 4", n)
	}

	// A retried park is a no-op: no second parked event.
	if err := f.store.Park(ctx, f.repoID, store.ParkArchived, true); err != nil {
		t.Fatalf("second Park: %v", err)
	}

	var parked int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM repository_events WHERE repository_id = $1 AND kind = 'parked'`,
		f.repoID).Scan(&parked); err != nil {
		t.Fatalf("count parked events: %v", err)
	}

	if parked != 1 {
		t.Errorf("parked events = %d, want 1", parked)
	}
}

func TestFindingEvents_AppendOnlyUnderApplicationRole(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.record(t, "k1", t0, missing("codeowners", "CODEOWNERS"))

	for _, stmt := range []string{
		`UPDATE finding_events SET to_status = 'compliant'`,
		`DELETE FROM finding_events`,
	} {
		_, err := f.pool.Exec(ctx, stmt)

		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: err = %v, want insufficient_privilege (42501)", stmt, err)
		}
	}
}
