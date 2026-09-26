//go:build integration

package shadow_test

import (
	"context"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/shadow"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest/v1sql"
)

// TestVerify_BackfillThenRecheck runs the shadow comparison against a
// real v1 → v2 migration: right after the backfill every finding is
// not_rechecked; a re-check that agrees with v1 matches; one that
// disagrees is unexplained.
func TestVerify_BackfillThenRecheck(t *testing.T) {
	t.Parallel()

	ctx := postgres.WithBackfillFreshness(context.Background(), 24*time.Hour)
	dsn := pgtest.Start(t)
	pgtest.SeedV1(t, dsn)

	db, err := postgres.OpenDB(dsn)
	if err != nil {
		t.Fatal(err)
	}

	checked := time.Now().Add(-time.Hour)
	for _, repo := range []string{"web", "api"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO repo_state (installation_id, owner, repo, last_checked_at, last_check_status, active)
			VALUES (7, 'acme', $1, $2, 'success', true)`, repo, checked); err != nil {
			t.Fatalf("seed repo_state: %v", err)
		}
	}

	for _, r := range []struct {
		repo, rule string
		actionable bool
	}{{"web", "codeowners", true}, {"web", "renovate", false}, {"api", "codeowners", true}} {
		if _, err := db.ExecContext(ctx, v1sql.UpsertRuleState, 7, "acme", r.repo, r.rule, "file", r.actionable, "v1hash"); err != nil {
			t.Fatalf("seed rule_state: %v", err)
		}
	}

	provider, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = provider.Close() })

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rep, err := shadow.Verify(ctx, db, db)
	if err != nil {
		t.Fatalf("Verify after backfill: %v", err)
	}

	// Backfilled compliant findings carry no reason, so they compare
	// directly; failing ones are migrated_from_v1.
	if rep.Repositories != 2 || rep.Counts[shadow.NotRechecked] != 2 || rep.Counts[shadow.Match] != 1 || !rep.OK() {
		t.Fatalf("after backfill: %+v", rep)
	}

	recheck := `UPDATE findings f SET status = $3, reason = $4
		FROM repositories r WHERE r.id = f.repository_id AND r.name = $1 AND f.rule_name = $2`

	if _, err := db.ExecContext(ctx, recheck, "web", "codeowners", "non_compliant", "file_missing"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, recheck, "api", "codeowners", "compliant", nil); err != nil {
		t.Fatal(err)
	}

	rep, err = shadow.Verify(ctx, db, db)
	if err != nil {
		t.Fatalf("Verify after recheck: %v", err)
	}

	if rep.Counts[shadow.Match] != 2 || len(rep.Unexplained) != 1 || rep.Unexplained[0].Repo != "api" {
		t.Fatalf("after recheck: counts %v, unexplained %+v", rep.Counts, rep.Unexplained)
	}
}
