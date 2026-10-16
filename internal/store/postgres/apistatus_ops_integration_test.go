//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// StatusInputs reads the daily compliance history (each UTC day's last
// snapshot within 30 days), repositories checked in the last 24h, the
// newest policy version's rollout and the last snapshot (IMPL-0031 7.4).
func TestStatusInputs_HistoryAndOperations(t *testing.T) {
	t.Parallel()

	f := newAPIFixture(t)
	seedTwoOrgs(t, f)

	ctx := t.Context()
	now := time.Now()
	day := 24 * time.Hour

	// acme/old was last checked two days ago; the store stamps real time,
	// so the owner moves the check back.
	old := f.discover(t, "acme", "old")
	f.evaluate(t, old, "old", "compliant")
	run(t, f.dsns.Owner, `UPDATE checks SET started_at = now() - interval '2 days', finished_at = now() - interval '2 days'
		WHERE check_key = 'old'`)

	pool, err := postgres.NewReadOnlyPool(ctx, f.dsns.Evaluator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := postgres.NewAPIReader(pool)

	in, err := r.StatusInputs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture activates its policy version before anything else.
	if in.Rollout == nil || in.Rollout.CompletedAt != nil || in.LastSnapshot != nil || len(in.History) != 0 {
		t.Fatalf("before any snapshot: rollout %+v, last snapshot %v, history %v", in.Rollout, in.LastSnapshot, in.History)
	}

	// Three seeded checks are fresh; acme/old's is two days old. d is
	// archived, so four repositories are active.
	if in.CheckedLastDay != 3 || in.ActiveRepositories != 4 {
		t.Errorf("checked_24h = %d, tracked = %d, want 3 and 4", in.CheckedLastDay, in.ActiveRepositories)
	}

	noon := now.UTC().Truncate(day).Add(-12 * time.Hour)
	for _, at := range []time.Time{noon.Add(-40 * day), noon.Add(-day - 6*time.Hour), noon.Add(-day), noon} {
		if _, err := f.store.InsertComplianceSnapshot(ctx, at); err != nil {
			t.Fatal(err)
		}
	}

	in, err = r.StatusInputs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	// 40 days ago is out of range; the 06:00 snapshot loses to its day's noon.
	want := []time.Time{noon.Add(-day), noon}
	if len(in.History) != len(want) {
		t.Fatalf("history = %+v, want points at %v", in.History, want)
	}

	// Three compliant of four measured: 75.0, floored like the shared query.
	for i, h := range in.History {
		if !h.At.Equal(want[i]) || h.Percent == nil || *h.Percent != 75 {
			t.Errorf("history[%d] = %v %+v, want %v 75", i, h.At, h.Percent, want[i])
		}
	}

	if in.LastSnapshot == nil || !in.LastSnapshot.Equal(noon) {
		t.Errorf("last snapshot = %v, want %v", in.LastSnapshot, noon)
	}

	if err := f.store.CompletePolicyRollout(ctx, testPolicyVersion); err != nil {
		t.Fatal(err)
	}

	in, err = r.StatusInputs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	if in.Rollout == nil || in.Rollout.CompletedAt == nil {
		t.Errorf("rollout = %+v, want complete", in.Rollout)
	}
}
