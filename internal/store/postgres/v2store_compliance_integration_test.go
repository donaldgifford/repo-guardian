//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// seedCompliance records one check per repository in org, each with the
// given outcomes, and returns the repository ids.
func (f *v2Fixture) seedRepos(t *testing.T, org string, n int, outcomes func(i int) []store.Outcome) []int64 {
	t.Helper()

	ctx := context.Background()
	ids := make([]int64, 0, n)

	for i := range n {
		res, err := f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{
			Org: org, Name: fmt.Sprintf("repo-%03d", i), InstallationID: testInstallation,
		})
		if err != nil {
			t.Fatalf("UpsertDiscovered: %v", err)
		}

		if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
			Key: fmt.Sprintf("%s-%d", org, i), RepositoryID: res.ID, InstallationID: testInstallation,
			Trigger: store.TriggerSchedule, PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0,
			Outcomes: outcomes(i),
		}); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}

		ids = append(ids, res.ID)
	}

	return ids
}

func notApplicable(name string) store.Outcome {
	return store.Outcome{
		RuleKind: findings.RuleKindFile, RuleName: name, Status: findings.StatusNotApplicable,
		Reason: findings.ReasonOutOfScopeRule, Evidence: findings.OutOfScopeRuleEvidence{},
	}
}

func TestCompliance_SharedQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	// 1000 repositories, one failing codeowners: 99.9, never 100.
	// renovate is not applicable everywhere: NULL, never 100.
	ids := f.seedRepos(t, "globex", 1000, func(i int) []store.Outcome {
		out := []store.Outcome{compliant("codeowners"), notApplicable("renovate")}
		if i == 0 {
			out[0] = missing("codeowners", "CODEOWNERS")
		}

		return out
	})

	// A parked repository's findings never count.
	if err := f.store.Park(ctx, ids[1], store.ParkAccessDenied, false); err != nil {
		t.Fatalf("Park: %v", err)
	}

	rows, err := f.store.Compliance(ctx, store.Scope{Orgs: []string{"GLOBEX"}})
	if err != nil {
		t.Fatalf("Compliance: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want codeowners and renovate", rows)
	}

	co, rn := rows[0], rows[1]
	if co.RuleName != "codeowners" || co.Compliant != 998 || co.NonCompliant != 1 || co.Percent == nil || *co.Percent != 99.8 {
		t.Errorf("codeowners = %+v (percent %v), want 998/1 at 99.8", co, co.Percent)
	}

	if rn.RuleName != "renovate" || rn.NotApplicable != 999 || rn.Percent != nil {
		t.Errorf("renovate = %+v, want 999 not applicable and a NULL percent", rn)
	}

	other, err := f.store.Compliance(ctx, store.Scope{Orgs: []string{"initech"}})
	if err != nil || len(other) != 0 {
		t.Errorf("out-of-scope Compliance = %+v, %v; want none", other, err)
	}
}

func TestCompliance_FlooredNotRounded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)

	// 2 of 3 compliant is 66.66…: floored to 66.6, never rounded to 66.7.
	f.seedRepos(t, "initech", 3, func(i int) []store.Outcome {
		if i == 0 {
			return []store.Outcome{missing("codeowners", "CODEOWNERS")}
		}

		return []store.Outcome{compliant("codeowners")}
	})

	rows, err := f.store.Compliance(ctx, store.Scope{Orgs: []string{"initech"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("Compliance = %+v, %v", rows, err)
	}

	if p := rows[0].Percent; p == nil || *p != 66.6 {
		t.Errorf("percent = %v, want 66.6", p)
	}
}

func TestCompliance_SnapshotStoresTheSharedCounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newV2Fixture(t)
	f.seedRepos(t, "initech", 3, func(i int) []store.Outcome {
		return []store.Outcome{compliant("codeowners"), missing("renovate", "renovate.json"), notApplicable("labels")}
	})

	at := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	n, err := f.store.InsertComplianceSnapshot(ctx, at)
	if err != nil || n != 3 {
		t.Fatalf("InsertComplianceSnapshot = %d, %v; want 3 rows", n, err)
	}

	if again, err := f.store.InsertComplianceSnapshot(ctx, at); err != nil || again != 0 {
		t.Errorf("retried snapshot = %d, %v; want 0 new rows", again, err)
	}

	rep, err := f.store.ComplianceReport(ctx, store.Scope{})
	if err != nil {
		t.Fatalf("ComplianceReport: %v", err)
	}

	if len(rep.Previous) != len(rep.Current) {
		t.Fatalf("snapshots %d, current %d", len(rep.Previous), len(rep.Current))
	}

	for i := range rep.Current {
		c, p := rep.Current[i], rep.Previous[i].ComplianceCount
		c.Percent = nil

		if c != p {
			t.Errorf("snapshot %+v != current %+v", p, c)
		}
	}

	if len(rep.Findings) != 3 || rep.Findings[0].RuleName != "renovate" || rep.Findings[0].Reason != findings.ReasonFileMissing {
		t.Errorf("findings = %+v, want three renovate file_missing", rep.Findings)
	}
}
