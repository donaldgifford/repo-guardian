//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestPostgresStore_ReportData_TrendComesFromThePerRuleHistory covers
// the three reads the report renders from, and the one subtlety in
// them.
//
// Previous is DISTINCT ON (org, rule_name) — the latest snapshot for
// EACH rule — not the rows from the latest snapshot run. Runs do not
// all cover the same rule set: a rule added last week has a shorter
// history than one added last year. Keying on a global max(snapshot_at)
// would silently drop every rule absent from the most recent run, so a
// newly-added rule would erase the trend of every older one.
func TestPostgresStore_ReportData_TrendComesFromThePerRuleHistory(t *testing.T) {
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

	// Snapshot 1: only codeowners exists, both repos failing.
	seed("one", []store.RuleState{{RuleName: "codeowners", Actionable: true}})
	seed("two", []store.RuleState{{RuleName: "codeowners", Actionable: true}})

	older := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)
	if _, err := s.InsertComplianceSnapshot(ctx, older); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// dependabot exists at snapshot 1 too, on repo one only.
	seed("one", []store.RuleState{
		{RuleName: "codeowners", Actionable: true},
		{RuleName: "dependabot", Actionable: true},
	})

	if _, err := s.InsertComplianceSnapshot(ctx, older.Add(time.Second)); err != nil {
		t.Fatalf("first snapshot (with dependabot): %v", err)
	}

	// Now dependabot is temporarily removed from the policy: the
	// delete-not-in in UpsertRuleStates drops its rows, so the NEXT
	// snapshot has no dependabot row at all.
	seed("one", []store.RuleState{{RuleName: "codeowners", Actionable: true}})

	newer := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err := s.InsertComplianceSnapshot(ctx, newer); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}

	// dependabot comes back, and one repo fixes codeowners.
	seed("one", []store.RuleState{
		{RuleName: "codeowners", Actionable: true},
		{RuleName: "dependabot", Actionable: true},
	})
	seed("two", []store.RuleState{{RuleName: "codeowners", Actionable: false}})

	data, err := s.ReportData(ctx)
	if err != nil {
		t.Fatalf("ReportData: %v", err)
	}

	// Findings: one/codeowners, one/dependabot. two is now compliant.
	if len(data.Findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(data.Findings), data.Findings)
	}

	// Ordered by owner, rule, repo — the renderer relies on SQL order so
	// an unchanged database regenerates a byte-identical report.
	if data.Findings[0].RuleName != "codeowners" || data.Findings[1].RuleName != "dependabot" {
		t.Errorf("findings are not ordered by rule: %+v", data.Findings)
	}

	if data.Findings[0].ActionableSince == nil {
		t.Error("ActionableSince is nil; the report cannot say how long a repo has been failing")
	}

	current := indexSnapshots(data.Current)
	if got := current["acme/codeowners"]; got.ActionableCount != 1 || got.TrackedCount != 2 {
		t.Errorf("current codeowners = %d/%d, want 1/2", got.ActionableCount, got.TrackedCount)
	}

	previous := indexSnapshots(data.Previous)

	// codeowners' latest row is from snapshot 2 (it was re-measured),
	// where both repos were still failing.
	if got := previous["acme/codeowners"]; got.ActionableCount != 2 {
		t.Errorf("previous codeowners actionable = %d, want 2", got.ActionableCount)
	}

	// dependabot is ABSENT from the newest snapshot — it was disabled
	// when that run happened — but it has history and is live again
	// now. This is the case DISTINCT ON exists for: keyed on a global
	// max(snapshot_at), this rule would lose its trend entirely.
	dependabot, ok := previous["acme/dependabot"]
	if !ok {
		t.Fatal("previous is missing dependabot; a rule absent from the newest run lost its whole history")
	}

	if dependabot.SnapshotAt.After(newer) {
		t.Errorf("dependabot previous is dated %v, want the older run at %v", dependabot.SnapshotAt, older)
	}

	if len(previous) != 2 {
		t.Errorf("previous rows = %d, want 2 (one per rule)", len(previous))
	}
}

// TestPostgresStore_ReportData_ZeroSnapshotsIsNotAnError covers a
// deployment that has never run the snapshot handler: there is a
// current state to report and simply no trend to show.
func TestPostgresStore_ReportData_ZeroSnapshotsIsNotAnError(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	data, err := s.ReportData(ctx)
	if err != nil {
		t.Fatalf("ReportData on an empty database = %v, want nil", err)
	}

	if len(data.Findings) != 0 || len(data.Current) != 0 || len(data.Previous) != 0 {
		t.Errorf("empty database produced %+v, want all empty", data)
	}
}

// indexSnapshots keys rows by "org/rule".
func indexSnapshots(rows []store.SnapshotRow) map[string]store.SnapshotRow {
	out := make(map[string]store.SnapshotRow, len(rows))
	for _, r := range rows {
		out[r.Org+"/"+r.RuleName] = r
	}

	return out
}
