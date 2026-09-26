//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest/v1sql"
)

// v1Data fingerprints every row of v1's tables.
func v1Data(t *testing.T, db *sql.DB) string {
	t.Helper()

	var out string
	if err := db.QueryRow(`SELECT
		(SELECT coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM repo_state r) || '/' ||
		(SELECT coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM rule_state r) || '/' ||
		(SELECT coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM compliance_snapshot r) || '/' ||
		(SELECT coalesce(md5(string_agg(r::text, '|' ORDER BY r::text)), '') FROM schema_migrations r)`).Scan(&out); err != nil {
		t.Fatalf("fingerprint v1 data: %v", err)
	}

	return out
}

// TestRollback_V1StillWorksAfterMigrate proves the rollback window
// (DESIGN-0025 § Rollback): after migrate, v1's tables are unchanged and
// a v1 image's Migrate, stale sweep and rule_state upsert still work.
func TestRollback_V1StillWorksAfterMigrate(t *testing.T) {
	t.Parallel()

	ctx := postgres.WithBackfillFreshness(context.Background(), 24*time.Hour)
	dsn := pgtest.Start(t)
	pgtest.SeedV1(t, dsn)

	db, up := openMigrator(t, dsn)
	seedV1Fleet(t, db)

	catalog, data := v1Catalog(t, db), v1Data(t, db)

	if _, err := up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if got := v1Catalog(t, db); got != catalog {
		t.Errorf("v1 schema changed by migrate:\nbefore %s\nafter  %s", catalog, got)
	}

	if got := v1Data(t, db); got != data {
		t.Error("v1 rows changed by migrate")
	}

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("v1 Migrate after v2 migrate: %v", err)
	}

	rows, err := db.QueryContext(ctx, v1sql.StaleRepos, time.Now().Add(-24*time.Hour), "abc", 100)
	if err != nil {
		t.Fatalf("v1 StaleRepos: %v", err)
	}

	n := 0
	for rows.Next() {
		n++
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("v1 StaleRepos rows: %v", err)
	}

	_ = rows.Close()

	if n == 0 {
		t.Error("v1 StaleRepos returned nothing; the seeded fleet is stale")
	}

	if _, err := db.ExecContext(ctx, v1sql.UpsertRuleState, 7, "acme", "checked", "codeowners", "file", false, "abc"); err != nil {
		t.Fatalf("v1 UpsertRuleState: %v", err)
	}
}
