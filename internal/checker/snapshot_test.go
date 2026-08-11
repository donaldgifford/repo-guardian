package checker_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// snapshotStore records the timestamps Take passes down.
type snapshotStore struct {
	store.Store

	at   []time.Time
	rows int
	err  error
}

func (s *snapshotStore) InsertComplianceSnapshot(_ context.Context, at time.Time) (int, error) {
	s.at = append(s.at, at)

	if s.err != nil {
		return 0, s.err
	}

	return s.rows, nil
}

// TestSnapshotTake_OneTimestampPerRun pins the property that makes the
// history queryable: every row of a snapshot shares one instant.
//
// The report groups by snapshot_at to compare a run against the one
// before it. If the timestamp came from the database per row, or were
// re-read per statement, a single run would scatter across several
// microsecond-apart instants and every trend query would have to bucket
// by time instead of grouping by a value.
func TestSnapshotTake_OneTimestampPerRun(t *testing.T) {
	fixed := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)

	st := &snapshotStore{rows: 12}

	taker := checker.NewSnapshotTaker(checker.SnapshotTakerOptions{
		Store:  st,
		Logger: slog.Default(),
		Now:    func() time.Time { return fixed },
	})

	if err := taker.Take(context.Background()); err != nil {
		t.Fatalf("Take() = %v, want nil", err)
	}

	if len(st.at) != 1 {
		t.Fatalf("InsertComplianceSnapshot calls = %d, want 1", len(st.at))
	}

	if !st.at[0].Equal(fixed) {
		t.Errorf("snapshot_at = %v, want %v", st.at[0], fixed)
	}
}

// TestSnapshotTake_FailurePropagates covers the scheduler contract: a
// failed snapshot must be visible as a handler error, not swallowed.
//
// Nothing retries inside the interval, and that is deliberate — a
// missing row leaves a gap in a daily series, which is obvious and
// harmless, whereas a retry loop against a database that is already
// struggling is neither.
func TestSnapshotTake_FailurePropagates(t *testing.T) {
	st := &snapshotStore{err: errors.New("deadlock detected")}

	taker := checker.NewSnapshotTaker(checker.SnapshotTakerOptions{
		Store:  st,
		Logger: slog.Default(),
	})

	err := taker.Take(context.Background())
	if err == nil {
		t.Fatal("Take() = nil on a store failure, want an error the scheduler can count")
	}

	if !errors.Is(err, st.err) {
		t.Errorf("Take() error = %v, want it to wrap the store error", err)
	}
}

// TestSnapshotTake_DefaultsToWallClock pins that production gets a real
// clock when Options.Now is nil.
func TestSnapshotTake_DefaultsToWallClock(t *testing.T) {
	st := &snapshotStore{}

	taker := checker.NewSnapshotTaker(checker.SnapshotTakerOptions{
		Store:  st,
		Logger: slog.Default(),
	})

	before := time.Now()

	if err := taker.Take(context.Background()); err != nil {
		t.Fatalf("Take() = %v, want nil", err)
	}

	if len(st.at) != 1 {
		t.Fatalf("InsertComplianceSnapshot calls = %d, want 1", len(st.at))
	}

	if st.at[0].Before(before) || st.at[0].After(time.Now()) {
		t.Errorf("snapshot_at = %v, want a time within this test's run", st.at[0])
	}
}
