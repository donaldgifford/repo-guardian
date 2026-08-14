//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// snapshotRow is one compliance_snapshot row read back for assertions.
type snapshotRow struct {
	actionable int
	tracked    int
}

// readSnapshot returns the rows written at exactly at, keyed by
// "org/rule".
func readSnapshot(ctx context.Context, t *testing.T, dsn string, at time.Time) map[string]snapshotRow {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(ctx) }()

	rows, err := conn.Query(ctx,
		`SELECT org, rule_name, actionable_count, tracked_count FROM compliance_snapshot WHERE snapshot_at = $1`, at)
	if err != nil {
		t.Fatalf("query snapshot: %v", err)
	}

	defer rows.Close()

	out := make(map[string]snapshotRow)

	for rows.Next() {
		var org, rule string

		var r snapshotRow

		if err := rows.Scan(&org, &rule, &r.actionable, &r.tracked); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}

		out[org+"/"+rule] = r
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate snapshot: %v", err)
	}

	return out
}

// TestPostgresStore_InsertComplianceSnapshot_PerRuleDenominator pins the
// decision that makes the report's percentages honest.
//
// The denominator is repos evaluated for THAT rule, not repos in the
// org. A rule scoped to a subset would otherwise be divided by the
// whole org and read as far more compliant than it is: here "dependabot"
// applies to two of four repos and fails on one of them, which is 50%,
// not the 25% an org-wide denominator would report.
//
// This is the gap recorded as the task 2.2 follow-up. The posture
// gauges cannot close it without a new series; the snapshot table has a
// per-rule row and can.
func TestPostgresStore_InsertComplianceSnapshot_PerRuleDenominator(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()

	seed := func(repo string, rules []store.RuleState) {
		t.Helper()

		if err := s.UpdateRepoState(ctx, &store.RepoState{
			InstallationID: 1, Owner: "acme", Repo: repo,
			LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed repo_state %s: %v", repo, err)
		}

		for i := range rules {
			rules[i].InstallationID = 1
			rules[i].Owner = "acme"
			rules[i].Repo = repo
			rules[i].RuleKind = "file"
			rules[i].PolicyVersion = "v1"
		}

		if err := s.UpsertRuleStates(ctx, 1, "acme", repo, rules); err != nil {
			t.Fatalf("seed rule_state %s: %v", repo, err)
		}
	}

	// codeowners applies to all four; dependabot only to two.
	seed("one", []store.RuleState{
		{RuleName: "codeowners", Actionable: true},
		{RuleName: "dependabot", Actionable: true},
	})
	seed("two", []store.RuleState{
		{RuleName: "codeowners", Actionable: false},
		{RuleName: "dependabot", Actionable: false},
	})
	seed("three", []store.RuleState{{RuleName: "codeowners", Actionable: true}})
	seed("four", []store.RuleState{{RuleName: "codeowners", Actionable: false}})

	at := time.Now().UTC().Truncate(time.Microsecond)

	rows, err := s.InsertComplianceSnapshot(ctx, at)
	if err != nil {
		t.Fatalf("InsertComplianceSnapshot: %v", err)
	}

	if rows != 2 {
		t.Errorf("rows written = %d, want 2 (one per rule)", rows)
	}

	got := readSnapshot(ctx, t, dsn, at)

	if want := (snapshotRow{actionable: 2, tracked: 4}); got["acme/codeowners"] != want {
		t.Errorf("codeowners = %+v, want %+v", got["acme/codeowners"], want)
	}

	if want := (snapshotRow{actionable: 1, tracked: 2}); got["acme/dependabot"] != want {
		t.Errorf("dependabot = %+v, want %+v; the denominator is repos the rule applies to, not repos in the org",
			got["acme/dependabot"], want)
	}
}

// TestPostgresStore_InsertComplianceSnapshot_ExcludesParkedAndIsIdempotent
// covers the two properties a retry depends on.
//
// Parked repos leave both halves of the ratio, exactly as they do in
// the posture query — a repo nobody can measure must not be counted as
// compliant or as failing. And re-running with the same timestamp
// writes nothing rather than erroring, so a handler that failed
// half-way can simply run again.
func TestPostgresStore_InsertComplianceSnapshot_ExcludesParkedAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()

	for _, repo := range []string{"live", "parked"} {
		if err := s.UpdateRepoState(ctx, &store.RepoState{
			InstallationID: 1, Owner: "acme", Repo: repo,
			LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed repo_state %s: %v", repo, err)
		}

		if err := s.UpsertRuleStates(ctx, 1, "acme", repo, []store.RuleState{{
			InstallationID: 1, Owner: "acme", Repo: repo,
			RuleName: "codeowners", RuleKind: "file", Actionable: true, PolicyVersion: "v1",
		}}); err != nil {
			t.Fatalf("seed rule_state %s: %v", repo, err)
		}
	}

	if err := s.Deactivate(ctx, 1, "acme", "parked"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)

	if _, err := s.InsertComplianceSnapshot(ctx, at); err != nil {
		t.Fatalf("InsertComplianceSnapshot: %v", err)
	}

	got := readSnapshot(ctx, t, dsn, at)

	if want := (snapshotRow{actionable: 1, tracked: 1}); got["acme/codeowners"] != want {
		t.Errorf("codeowners = %+v, want %+v; the parked repo is still being counted", got["acme/codeowners"], want)
	}

	// Same timestamp again: a retry after a partial failure.
	rows, err := s.InsertComplianceSnapshot(ctx, at)
	if err != nil {
		t.Fatalf("second InsertComplianceSnapshot: %v", err)
	}

	if rows != 0 {
		t.Errorf("rows written on retry = %d, want 0; the insert is not idempotent", rows)
	}
}

// TestPostgresStore_InsertComplianceSnapshot_EmptyFleetWritesNothing
// covers the freshly migrated deployment: no rule_state rows means no
// snapshot rows, and no error. An empty history is honest; a row of
// zeros would assert a fleet was measured and found compliant.
func TestPostgresStore_InsertComplianceSnapshot_EmptyFleetWritesNothing(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	rows, err := s.InsertComplianceSnapshot(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("InsertComplianceSnapshot on an empty fleet = %v, want nil", err)
	}

	if rows != 0 {
		t.Errorf("rows written = %d, want 0", rows)
	}
}
