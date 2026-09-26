//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// openMigrator opens dsn and returns a migrator plus a separate handle
// for assertions. Both are closed when t finishes.
func openMigrator(t *testing.T, dsn string) (*sql.DB, func(context.Context) (int, error)) {
	t.Helper()

	db, err := postgres.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	migDB, err := postgres.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open migrator db: %v", err)
	}

	provider, err := postgres.NewMigrator(migDB)
	if err != nil {
		t.Fatalf("new migrator: %v", err)
	}

	t.Cleanup(func() { _ = provider.Close() })

	up := func(ctx context.Context) (int, error) {
		res, err := provider.Up(ctx)

		return len(res), err
	}

	return db, up
}

func adoptedAt(t *testing.T, db *sql.DB) bool {
	t.Helper()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM v2_meta WHERE key = $1`, postgres.MetaAdoptedV1At).Scan(&n); err != nil {
		t.Fatalf("read v2_meta: %v", err)
	}

	return n == 1
}

func TestMigrate_Adoption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seed        func(t *testing.T, dsn string, db *sql.DB)
		wantErr     error
		wantAdopted bool
	}{
		{
			name: "fresh database is a no-op",
			seed: func(*testing.T, string, *sql.DB) {},
		},
		{
			name: "clean v1 at version 3 is adopted",
			seed: func(t *testing.T, dsn string, _ *sql.DB) {
				t.Helper()
				pgtest.SeedV1(t, dsn)
			},
			wantAdopted: true,
		},
		{
			name: "v1 at version 2 is refused",
			seed: func(t *testing.T, dsn string, db *sql.DB) {
				t.Helper()
				pgtest.SeedV1(t, dsn)
				mustExec(t, db, `UPDATE schema_migrations SET version = 2`)
			},
			wantErr: postgres.ErrV1SchemaNotAdoptable,
		},
		{
			name: "dirty v1 is refused",
			seed: func(t *testing.T, dsn string, db *sql.DB) {
				t.Helper()
				pgtest.SeedV1(t, dsn)
				mustExec(t, db, `UPDATE schema_migrations SET dirty = true`)
			},
			wantErr: postgres.ErrV1SchemaNotAdoptable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			dsn := pgtest.Start(t)
			db, up := openMigrator(t, dsn)
			tt.seed(t, dsn, db)

			_, err := up(ctx)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Up error = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Up: %v", err)
			}

			if got := adoptedAt(t, db); got != tt.wantAdopted {
				t.Errorf("adopted = %t, want %t", got, tt.wantAdopted)
			}

			// A second run applies nothing.
			n, err := up(ctx)
			if err != nil || n != 0 {
				t.Errorf("re-run applied %d migrations, err %v; want 0, nil", n, err)
			}
		})
	}
}

func TestCheckV1Idle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dsn := pgtest.Start(t)
	db, _ := openMigrator(t, dsn)

	if err := postgres.CheckV1Idle(ctx, db); err != nil {
		t.Fatalf("fresh database: %v", err)
	}

	pgtest.SeedV1(t, dsn)
	mustExec(t, db, `INSERT INTO repo_state (installation_id, owner, repo, last_checked_at)
		VALUES (1, 'acme', 'old', now() - interval '1 hour')`)

	if err := postgres.CheckV1Idle(ctx, db); err != nil {
		t.Fatalf("idle v1: %v", err)
	}

	mustExec(t, db, `INSERT INTO repo_state (installation_id, owner, repo, last_checked_at)
		VALUES (1, 'acme', 'recent', now())`)

	if err := postgres.CheckV1Idle(ctx, db); !errors.Is(err, postgres.ErrV1Running) {
		t.Fatalf("running v1: err = %v, want ErrV1Running", err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestRequireSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dsn := pgtest.Start(t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.RequireSchema(ctx, pool, postgres.SchemaVersion); !errors.Is(err, postgres.ErrSchemaTooOld) {
		t.Fatalf("unmigrated: err = %v, want ErrSchemaTooOld", err)
	}

	_, up := openMigrator(t, dsn)
	if _, err := up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	if err := postgres.RequireSchema(ctx, pool, postgres.SchemaVersion); err != nil {
		t.Fatalf("migrated: %v", err)
	}

	if err := postgres.RequireSchema(ctx, pool, postgres.SchemaVersion+1); !errors.Is(err, postgres.ErrSchemaTooOld) {
		t.Fatalf("future version: err = %v, want ErrSchemaTooOld", err)
	}
}

// v1Catalog renders every v1 table's columns, constraints, indexes and
// grants, plus the default privileges, as one comparable string: the
// pg_dump --schema-only surface for the objects v2 must never touch.
func v1Catalog(t *testing.T, db *sql.DB) string {
	t.Helper()

	const q = `
SELECT coalesce(string_agg(line, E'\n' ORDER BY line), '') FROM (
  SELECT c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
         || CASE WHEN a.attnotnull THEN ' not null' ELSE '' END
         || coalesce(' default ' || pg_get_expr(d.adbin, d.adrelid), '') AS line
  FROM pg_class c
  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
  LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
  WHERE c.relnamespace = 'public'::regnamespace AND c.relname = ANY($1)
  UNION ALL
  SELECT conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE conrelid::regclass::text = ANY($1)
  UNION ALL
  SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = ANY($1)
  UNION ALL
  SELECT relname || ' acl ' || coalesce(relacl::text, '') FROM pg_class
  WHERE relnamespace = 'public'::regnamespace AND relname = ANY($1)
  UNION ALL
  SELECT 'default acl ' || defaclacl::text FROM pg_default_acl
) s`

	tables := "{repo_state,rule_state,compliance_snapshot,schema_migrations}"

	var out string
	if err := db.QueryRowContext(context.Background(), q, tables).Scan(&out); err != nil {
		t.Fatalf("read v1 catalog: %v", err)
	}

	return out
}

func TestMigrate_DownToAdoptionLeavesV1Untouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dsn := pgtest.AppRole(t, pgtest.Start(t))
	pgtest.SeedV1(t, dsn)

	db, _ := openMigrator(t, dsn)
	before := v1Catalog(t, db)

	migDB, err := postgres.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	provider, err := postgres.NewMigrator(migDB)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}

	t.Cleanup(func() { _ = provider.Close() })

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}

	if _, err := provider.DownTo(ctx, 1); err != nil {
		t.Fatalf("down to 1: %v", err)
	}

	if after := v1Catalog(t, db); after != before {
		t.Errorf("v1 schema changed across up/down:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	var v2Tables int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename = ANY('{findings,checks,repositories}')`).
		Scan(&v2Tables); err != nil {
		t.Fatalf("count v2 tables: %v", err)
	}

	if v2Tables != 0 {
		t.Errorf("%d v2 tables survived down", v2Tables)
	}
}
