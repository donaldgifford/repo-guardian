//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// StatusInputs reads the daily compliance history (each UTC day's last
// snapshot within 30 days), repositories checked in the last 24h, the
// newest policy version's rollout and the last snapshot (IMPL-0031 7.4).
func TestStatusInputs_HistoryAndOperations(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	seedTwoOrgs(t, f)

	ctx := t.Context()
	now := t0.Add(time.Hour)
	day := 24 * time.Hour

	old, err := f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: "acme", Name: "old", InstallationID: testInstallation})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
		Key: "old", RepositoryID: old.ID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: t0.Add(-2 * day), FinishedAt: t0.Add(-2 * day), Outcomes: []store.Outcome{compliant("codeowners")},
	}); err != nil {
		t.Fatal(err)
	}

	pool, err := postgres.NewReadOnlyPool(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := postgres.NewAPIReader(pool)

	in, err := r.StatusInputs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	if in.Rollout != nil || in.LastSnapshot != nil || len(in.History) != 0 {
		t.Fatalf("before any version or snapshot: rollout %+v, last snapshot %v, history %v", in.Rollout, in.LastSnapshot, in.History)
	}

	// The three seeded checks finished at t0; acme/old's two days ago.
	if in.CheckedLastDay != 3 || in.ActiveRepositories != 4 {
		t.Errorf("checked_24h = %d, tracked = %d, want 3 and 4", in.CheckedLastDay, in.ActiveRepositories)
	}

	for _, at := range []time.Time{t0.Add(-40 * day), t0.Add(-2*day - 6*time.Hour), t0.Add(-2 * day), t0.Add(-day)} {
		if _, err := f.store.InsertComplianceSnapshot(ctx, at); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := f.store.RecordPolicyVersion(ctx, "v2:ops", store.PolicySummary{}); err != nil {
		t.Fatal(err)
	}

	in, err = r.StatusInputs(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	// 40 days ago is out of range; the 06:00 snapshot loses to its day's noon.
	want := []time.Time{t0.Add(-2 * day), t0.Add(-day)}
	if len(in.History) != len(want) {
		t.Fatalf("history = %+v, want points at %v", in.History, want)
	}

	// Three compliant of four measured: 75.0, floored like the shared query.
	for i, h := range in.History {
		if !h.At.Equal(want[i]) || h.Percent == nil || *h.Percent != 75 {
			t.Errorf("history[%d] = %v %+v, want %v 75", i, h.At, h.Percent, want[i])
		}
	}

	if in.LastSnapshot == nil || !in.LastSnapshot.Equal(t0.Add(-day)) {
		t.Errorf("last snapshot = %v, want %v", in.LastSnapshot, t0.Add(-day))
	}

	if in.Rollout == nil || in.Rollout.CompletedAt != nil {
		t.Fatalf("rollout = %+v, want in progress", in.Rollout)
	}

	if err := f.store.CompletePolicyRollout(ctx, "v2:ops"); err != nil {
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
