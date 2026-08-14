//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/report"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestReport_MatchesDatabaseTruth is the Phase 4 acceptance criterion:
// a generated report for a seeded org matches the database exactly, on
// identity, since-dates and percentages.
//
// The golden tests pin the FORMAT against hand-built view models and
// the store integration tests pin the QUERIES against a real database.
// Neither catches a mismatch at the seam — a renderer reading the wrong
// field, or a query whose column order silently changed — because each
// half supplies the other half's input itself. This test is the only
// one where the numbers come out of Postgres and the assertions are
// made against what an operator will read.
func TestReport_MatchesDatabaseTruth(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	now := time.Now().UTC()

	seed := func(repo string, rules []store.RuleState) {
		t.Helper()

		if err := s.UpdateRepoState(ctx, &store.RepoState{
			InstallationID: 7, Owner: "acme", Repo: repo,
			LastCheckedAt: &now, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed repo_state %s: %v", repo, err)
		}

		for i := range rules {
			rules[i].InstallationID = 7
			rules[i].Owner = "acme"
			rules[i].Repo = repo
			rules[i].RuleKind = "file"
			rules[i].PolicyVersion = "v1"
		}

		if err := s.UpsertRuleStates(ctx, 7, "acme", repo, rules); err != nil {
			t.Fatalf("seed rule_state %s: %v", repo, err)
		}
	}

	// Four repos, three of them failing codeowners. 1 of 4 compliant is
	// 25.0% — a number that is wrong under any off-by-one in either the
	// FILTER or the denominator.
	for _, repo := range []string{"alpha", "bravo", "charlie"} {
		seed(repo, []store.RuleState{{RuleName: "codeowners", Actionable: true}})
	}

	// The snapshot is taken while all four are failing, so the trend
	// against today's three reads "1 fewer".
	seed("delta", []store.RuleState{{RuleName: "codeowners", Actionable: true}})

	yesterday := now.Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err := s.InsertComplianceSnapshot(ctx, yesterday); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// delta gets fixed after the snapshot.
	seed("delta", []store.RuleState{{RuleName: "codeowners", Actionable: false}})

	data, err := s.ReportData(ctx)
	if err != nil {
		t.Fatalf("ReportData: %v", err)
	}

	renderer, err := report.New(report.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("report.New: %v", err)
	}

	orgs := renderer.Build(data)
	if len(orgs) != 1 {
		t.Fatalf("Build() = %d orgs, want 1: %+v", len(orgs), orgs)
	}

	body, err := renderer.Render(orgs[0])
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The since-date comes from the actionable_since the upsert stamped,
	// not from the report's own clock, so it is asserted as the date the
	// database chose.
	since := data.Findings[0].ActionableSince
	if since == nil {
		t.Fatal("ActionableSince is nil; the report cannot say how long a repo has been failing")
	}

	want := []string{
		"# Compliance report: acme",
		// 1 of 4 repos compliant.
		"1 of 4 rule evaluations pass across 1 rule(s).",
		// Failing 3, applies to 4, 25.0%.
		"| codeowners | file | 3 | 4 | 25.0% |",
		// Four failing yesterday, three today.
		fmt.Sprintf("1 fewer since %s", yesterday.Format("2006-01-02")),
		// Every failing repo named, and only those.
		fmt.Sprintf("| alpha | codeowners | %s |", since.UTC().Format("2006-01-02")),
		"| bravo | codeowners |",
		"| charlie | codeowners |",
	}

	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("report is missing %q\n%s", w, body)
		}
	}

	// delta complies, so it must not be listed. This is the assertion
	// that a findings query dropping its WHERE actionable would break.
	if strings.Contains(body, "| delta |") {
		t.Errorf("report lists delta, which satisfies every rule\n%s", body)
	}

	// No linker was configured, so the PR column must be absent
	// entirely rather than rendered empty.
	if strings.Contains(body, "Open PR") {
		t.Errorf("report rendered a PR column with no linker\n%s", body)
	}
}

// TestReport_ParkedReposAreExcludedFromTheNumbers pins that a parked
// repository changes the denominator rather than counting as compliant.
//
// A repository nobody can measure — archived, forked, or unreadable by
// the App (INV-0015) — is neither compliant nor failing. Counting it as
// either makes the percentage a guess, and counting it as compliant is
// the flattering direction, which is the one that gets believed.
func TestReport_ParkedReposAreExcludedFromTheNumbers(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	now := time.Now().UTC()

	seed := func(repo string, active bool, actionable bool) {
		t.Helper()

		rs := &store.RepoState{
			InstallationID: 7, Owner: "acme", Repo: repo,
			LastCheckedAt: &now, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}
		if err := s.UpdateRepoState(ctx, rs); err != nil {
			t.Fatalf("seed repo_state %s: %v", repo, err)
		}

		if err := s.UpsertRuleStates(ctx, 7, "acme", repo, []store.RuleState{{
			InstallationID: 7, Owner: "acme", Repo: repo,
			RuleName: "codeowners", RuleKind: "file",
			Actionable: actionable, PolicyVersion: "v1",
		}}); err != nil {
			t.Fatalf("seed rule_state %s: %v", repo, err)
		}

		if !active {
			if err := s.Deactivate(ctx, 7, "acme", repo); err != nil {
				t.Fatalf("deactivate %s: %v", repo, err)
			}
		}
	}

	seed("live", true, true)
	seed("archived", false, true)

	data, err := s.ReportData(ctx)
	if err != nil {
		t.Fatalf("ReportData: %v", err)
	}

	renderer, err := report.New(report.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("report.New: %v", err)
	}

	body, err := renderer.Render(renderer.Build(data)[0])
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// One tracked repo, not two: the archived one left the denominator
	// rather than joining the compliant count.
	if !strings.Contains(body, "| codeowners | file | 1 | 1 | 0.0% |") {
		t.Errorf("parked repo is still in the numbers\n%s", body)
	}

	if strings.Contains(body, "| archived |") {
		t.Errorf("report lists a parked repository as a finding\n%s", body)
	}
}
