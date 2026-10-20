//go:build integration

package postgres_test

import (
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// historyFixture is seedTwoOrgs with three snapshot runs over two UTC
// days, acme-pay's second run on day one moved two hours later so the
// orgs' snapshot times differ within the day, and acme-web/b turning
// compliant between the runs.
func historyFixture(t *testing.T) (*postgres.APIReader, *store.HistoryFilter, []store.ControlPosture) {
	t.Helper()

	f := newAPIFixture(t)
	ids := seedTwoOrgs(t, f)
	ctx := t.Context()

	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	at2 := day.Add(10 * time.Hour)

	for _, at := range []time.Time{day.Add(time.Hour), at2} {
		if _, err := f.store.InsertComplianceSnapshot(ctx, at); err != nil {
			t.Fatal(err)
		}
	}

	run(t, f.dsns.Owner, `UPDATE compliance_snapshots SET snapshot_at = snapshot_at + interval '2 hours'
		WHERE lower(org) = 'acme-pay' AND snapshot_at = '`+at2.Format(time.RFC3339)+`'`)

	f.evaluate(t, ids["acme-web/b"], "w3", "compliant")

	if _, err := f.store.InsertComplianceSnapshot(ctx, day.Add(27*time.Hour)); err != nil {
		t.Fatal(err)
	}

	pool, err := postgres.NewReadOnlyPool(ctx, f.dsns.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	current, err := f.store.ControlCompliance(ctx, nil, store.LineageAll)
	if err != nil {
		t.Fatal(err)
	}

	return postgres.NewAPIReader(pool), &store.HistoryFilter{Since: day.Add(-72 * time.Hour)}, current
}

// sumBy folds rows into one point per key with ControlPosture.Plus.
func sumBy(rows []store.ComplianceSnapshot, key func(*store.ComplianceSnapshot) string) map[string]store.ControlPosture {
	out := map[string]store.ControlPosture{}

	for i := range rows {
		k := key(&rows[i])
		if acc, ok := out[k]; ok {
			out[k] = acc.Plus(&rows[i].ControlPosture)
		} else {
			p := rows[i].ControlPosture
			out[k] = p.Plus(&store.ControlPosture{Org: p.Org, ControlID: p.ControlID, Decision: p.Decision})
		}
	}

	return out
}

func samePosture(a, b *store.ControlPosture) bool {
	eq := func(x, y *float64) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }

	return a.Compliant == b.Compliant && a.NonCompliant == b.NonCompliant && a.Pending == b.Pending && a.Stale == b.Stale &&
		a.UnmeasurableAccessDenied == b.UnmeasurableAccessDenied && a.UnmeasurableSuspended == b.UnmeasurableSuspended &&
		a.UnmeasurableConflict == b.UnmeasurableConflict && a.NotApplicable == b.NotApplicable && a.Unknown == b.Unknown &&
		a.Excluded == b.Excluded && a.Assigned == b.Assigned && eq(a.Percent, b.Percent) && eq(a.Coverage, b.Coverage)
}

// TestComplianceHistory_FleetIsTheSumOfTheRows is IMPL-0030 7.6: the
// fleet series is aggregate=none summed per UTC day with
// ControlPosture.Plus, one point per day although acme-pay's snapshot
// ran at another time that day.
func TestComplianceHistory_FleetIsTheSumOfTheRows(t *testing.T) {
	t.Parallel()

	r, f, _ := historyFixture(t)
	ctx := t.Context()

	rows, err := r.ComplianceHistory(ctx, store.ScopeAll(), f)
	if err != nil {
		t.Fatal(err)
	}

	want := sumBy(rows, func(s *store.ComplianceSnapshot) string { return s.SnapshotAt.UTC().Format(time.DateOnly) })

	fleet, err := r.ComplianceHistory(ctx, store.ScopeAll(), &store.HistoryFilter{Since: f.Since, Aggregate: store.HistoryFleet})
	if err != nil {
		t.Fatal(err)
	}

	if len(fleet) != 2 || len(want) != 2 {
		t.Fatalf("fleet = %d points, rows summed = %d days; want 2 each", len(fleet), len(want))
	}

	for i := range fleet {
		p := &fleet[i]
		w := want[p.SnapshotAt.UTC().Format(time.DateOnly)]

		if p.Org != "" || p.ControlID != "" || !samePosture(&p.ControlPosture, &w) {
			t.Errorf("fleet %s = %+v, want %+v", p.SnapshotAt, p.ControlPosture, w)
		}
	}

	if fleet[0].NonCompliant != 1 || fleet[1].NonCompliant != 0 {
		t.Errorf("fleet non-compliant = %d then %d, want b failing on day one only", fleet[0].NonCompliant, fleet[1].NonCompliant)
	}
}

// TestComplianceHistory_OrgAgreesWithTheComplianceQuery: the latest
// day's org series equals ControlCompliance summed per org.
func TestComplianceHistory_OrgAgreesWithTheComplianceQuery(t *testing.T) {
	t.Parallel()

	r, f, current := historyFixture(t)

	byOrg, err := r.ComplianceHistory(t.Context(), store.ScopeAll(), &store.HistoryFilter{Since: f.Since, Aggregate: store.HistoryOrg})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]store.ControlPosture{}

	for i := range current {
		k := strings.ToLower(current[i].Org)
		if acc, ok := want[k]; ok {
			want[k] = acc.Plus(&current[i])
		} else {
			want[k] = current[i]
		}
	}

	latest := byOrg[len(byOrg)-1].SnapshotAt
	seen := 0

	for i := range byOrg {
		p := &byOrg[i]
		if !p.SnapshotAt.Equal(latest) {
			continue
		}

		seen++

		if w := want[strings.ToLower(p.Org)]; p.Org == "" || p.ControlID != "" || !samePosture(&p.ControlPosture, &w) {
			t.Errorf("org %q on %s = %+v, want %+v", p.Org, latest, p.ControlPosture, w)
		}
	}

	if seen != len(want) {
		t.Errorf("latest day has %d org points, want %d", seen, len(want))
	}

	byControl, err := r.ComplianceHistory(t.Context(), store.ScopeAll(), &store.HistoryFilter{Since: f.Since, Aggregate: store.HistoryControl})
	if err != nil {
		t.Fatal(err)
	}

	if len(byControl) != 2 || byControl[0].ControlID != "codeowners" || byControl[0].Org != "" {
		t.Errorf("control series = %+v, want one codeowners point per day", byControl)
	}
}
