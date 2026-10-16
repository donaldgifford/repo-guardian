//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestCompliance_ReportAndSnapshotAgree pins that the shared compliance
// query and InsertComplianceSnapshot compute identical counts and
// percentages for the same data. Phase 15 extends it to the API.
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

	var lines []store.ComplianceCount

	for _, c := range before.Current {
		if c.Org == "initech" {
			lines = append(lines, c)
		}
	}

	if _, err := f.store.InsertComplianceSnapshot(ctx, time.Now()); err != nil {
		t.Fatalf("InsertComplianceSnapshot: %v", err)
	}

	after, err := f.store.ComplianceReport(ctx, store.Scope{})
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	if len(after.Previous) != len(lines) {
		t.Fatalf("snapshot rows %d, query rows %d", len(after.Previous), len(lines))
	}

	for i, line := range lines {
		sn := after.Previous[i]

		if sn.RuleName != line.RuleName || sn.Kind != line.Kind || sn.Compliant != line.Compliant ||
			sn.NonCompliant != line.NonCompliant || sn.NotApplicable != line.NotApplicable || sn.Unknown != line.Unknown {
			t.Errorf("rule %s: snapshot %+v, query %+v", line.RuleName, sn, line)
		}

		// The snapshot's counts, run back through the shared query's
		// formula, give the query's percentage exactly.
		snapPct := snapshotPercent(sn.Compliant, sn.NonCompliant)

		if (line.Percent == nil) != (snapPct == nil) || (snapPct != nil && *line.Percent != *snapPct) {
			t.Errorf("rule %s: query %v, snapshot %v", line.RuleName, line.Percent, snapPct)
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
