//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// failingSince adds a repository whose codeowners finding has failed
// since at.
func failingSince(t *testing.T, e *endpoints, org, name string, at time.Time) {
	t.Helper()

	ctx := t.Context()

	res, err := e.f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: org, Name: name, InstallationID: testInstallation})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.f.store.RecordCheck(ctx, &store.CheckRecord{
		Key: "age-" + name, RepositoryID: res.ID, InstallationID: testInstallation, Trigger: store.TriggerSchedule,
		PolicyVersion: "v2:test", StartedAt: at, FinishedAt: at, Outcomes: []store.Outcome{missing("codeowners", "CODEOWNERS")},
	}); err != nil {
		t.Fatal(err)
	}
}

// Summary.finding_age buckets every non-compliant finding by its
// status_since against the API clock, lists every bucket in order with
// zeros, and is scoped.
func TestSummary_FindingAge(t *testing.T) {
	t.Parallel()

	e := newEndpoints(t)

	day := 24 * time.Hour
	for name, age := range map[string]time.Duration{
		"h12": 12 * time.Hour, "d3": 3 * day, "d15": 15 * day, "d60": 60 * day, "d120": 120 * day, "d200": 200 * day,
	} {
		failingSince(t, e, "acme-pay", name, t0.Add(-age))
	}

	tests := []struct {
		group string
		want  map[string]int
	}{
		// acme-web/b failed at t0, so <1d holds it and h12.
		{group: "platform", want: map[string]int{"<1d": 2, "1-7d": 1, "7-30d": 1, "30-90d": 1, "90-180d": 1, "180d+": 1}},
		{group: "team-web", want: map[string]int{"<1d": 1, "1-7d": 0, "7-30d": 0, "30-90d": 0, "90-180d": 0, "180d+": 0}},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			t.Parallel()

			sum := decodeAs[gen.Summary](t, e.get(t, "/summary", tt.group))
			if len(sum.FindingAge) != len(store.FindingAgeBuckets) {
				t.Fatalf("finding_age = %+v, want %d buckets", sum.FindingAge, len(store.FindingAgeBuckets))
			}

			for i, b := range sum.FindingAge {
				if b.Bucket != store.FindingAgeBuckets[i] || b.Count != tt.want[b.Bucket] {
					t.Errorf("finding_age[%d] = %+v, want %s=%d", i, b, store.FindingAgeBuckets[i], tt.want[store.FindingAgeBuckets[i]])
				}
			}
		})
	}
}
