//go:build integration

package postgres_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/report"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

func renderOrg(t *testing.T, rep *store.ComplianceReport, org string) (report.Org, string) {
	t.Helper()

	r, err := report.New(report.Options{Now: func() time.Time { return v1T }})
	if err != nil {
		t.Fatalf("report.New: %v", err)
	}

	for _, o := range r.Build(rep) {
		if o.Name == org {
			body, err := r.Render(o)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			return o, body
		}
	}

	t.Fatalf("no report for org %s", org)

	return report.Org{}, ""
}

// TestReport_BackfilledFindingsKeepTheirV1Since is Phase 8's acceptance
// criterion: on a seeded, migrated database the report shows each
// backfilled failure as migrated_from_v1 with v1's original date.
func TestReport_BackfilledFindingsKeepTheirV1Since(t *testing.T) {
	t.Parallel()

	m := newMigratedV1(t)

	pool, err := pgxpool.New(context.Background(), m.dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	s := postgres.NewV2Store(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rep, err := s.ComplianceReport(context.Background(), store.Scope{Orgs: []string{"acme"}})
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	_, body := renderOrg(t, rep, "acme")

	for _, want := range []string{
		"| checked | codeowners | migrated_from_v1 | " + v1Since.Format("2006-01-02") + " |",
		"| checked | renovate | migrated_from_v1 | " + v1Upd.Format("2006-01-02") + " |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q:\n%s", want, body)
		}
	}

	// dup was dropped as a case collision and every parked repository is
	// excluded: neither may appear.
	for _, absent := range []string{"| dup |", "| denied |"} {
		if strings.Contains(body, absent) {
			t.Errorf("report names %q:\n%s", absent, body)
		}
	}
}

// TestCompliance_ReportAndSnapshotAgree pins that report.Build and
// InsertComplianceSnapshot compute identical counts and percentages for
// the same data. Phase 15 extends it to the API.
func TestCompliance_ReportAndSnapshotAgree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.seedRepos(t, "initech", 7, func(i int) []store.Outcome {
		out := []store.Outcome{compliant("codeowners"), compliant("renovate"), notApplicable("labels")}
		if i%3 == 0 {
			out[0] = missing("codeowners", "CODEOWNERS")
		}

		if i == 6 {
			out[1] = missing("renovate", "renovate.json")
		}

		return out
	})

	before, err := f.store.ComplianceReport(ctx, store.Scope{})
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	org, _ := renderOrg(t, before, "initech")

	if _, err := f.store.InsertComplianceSnapshot(ctx, time.Now()); err != nil {
		t.Fatalf("InsertComplianceSnapshot: %v", err)
	}

	after, err := f.store.ComplianceReport(ctx, store.Scope{})
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	if len(after.Previous) != len(org.Rules) {
		t.Fatalf("snapshot rows %d, report rules %d", len(after.Previous), len(org.Rules))
	}

	for i, line := range org.Rules {
		sn := after.Previous[i]

		if sn.RuleName != line.Name || string(sn.Kind) != line.Kind || sn.Compliant != line.Compliant ||
			sn.NonCompliant != line.NonCompliant || sn.NotApplicable != line.NotApplicable || sn.Unknown != line.Unknown {
			t.Errorf("rule %s: snapshot %+v, report %+v", line.Name, sn, line)
		}

		// The snapshot's counts, run back through the shared query's
		// formula, give the report's percentage exactly.
		reportPct, ok := line.CompliantPercent()
		snapPct := snapshotPercent(sn.Compliant, sn.NonCompliant)

		if ok != (snapPct != nil) || (ok && reportPct != *snapPct) {
			t.Errorf("rule %s: report %v (ok %t), snapshot %v", line.Name, reportPct, ok, snapPct)
		}
	}
}

// snapshotPercent re-derives a percentage from stored counts with the
// shared query's floor.
func snapshotPercent(compliant, nonCompliant int) *float64 {
	if compliant+nonCompliant == 0 {
		return nil
	}

	v := float64(compliant*1000/(compliant+nonCompliant)) / 10

	return &v
}
