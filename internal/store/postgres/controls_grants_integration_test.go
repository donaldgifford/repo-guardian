//go:build integration

package postgres_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// TestControlsChain_WorkersProbeTheSchemaAfterMigrateAlone is issue #232:
// a database prepared the way CNPG prepares it (the app owner owns the
// database and so public, public grants PUBLIC nothing, managed.roles
// creates the worker roles with no grants) gets everything
// RequireControlsSchema needs from the migrate path alone. pgtest's
// ControlsRoles grants USAGE by hand, which is why CI never saw the
// crash loop.
func TestControlsChain_WorkersProbeTheSchemaAfterMigrateAlone(t *testing.T) {
	ctx := t.Context()
	admin := pgtest.Start(t)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer conn.Close(context.Background())

	for _, stmt := range []string{
		"CREATE ROLE " + pgtest.OwnerRole + " LOGIN PASSWORD '" + pgtest.OwnerRole + "' NOSUPERUSER NOCREATEROLE",
		"CREATE ROLE " + pgtest.EvaluatorRole + " LOGIN PASSWORD '" + pgtest.EvaluatorRole + "' NOSUPERUSER",
		"CREATE ROLE " + pgtest.RemediatorRole + " LOGIN PASSWORD '" + pgtest.RemediatorRole + "' NOSUPERUSER",
		"CREATE ROLE " + pgtest.AllRole + " LOGIN PASSWORD '" + pgtest.AllRole + "' NOSUPERUSER IN ROLE " +
			pgtest.EvaluatorRole + ", " + pgtest.RemediatorRole,
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	var db string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&db); err != nil {
		t.Fatalf("current_database: %v", err)
	}

	if _, err := conn.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{db}.Sanitize()+" OWNER TO "+pgtest.OwnerRole); err != nil {
		t.Fatalf("alter database owner: %v", err)
	}

	as := func(role string) string {
		u, err := url.Parse(admin)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}

		u.User = url.UserPassword(role, role)

		return u.String()
	}

	sqlDB, err := postgres.OpenDB(as(pgtest.OwnerRole))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	provider, err := postgres.NewControlsMigrator(sqlDB)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}

	defer provider.Close()

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply controls chain: %v", err)
	}

	for _, role := range []string{pgtest.EvaluatorRole, pgtest.RemediatorRole, pgtest.AllRole} {
		t.Run(role, func(t *testing.T) {
			pool, err := pgxpool.New(ctx, as(role))
			if err != nil {
				t.Fatalf("pool: %v", err)
			}

			defer pool.Close()

			if err := postgres.RequireControlsSchema(ctx, pool, postgres.ControlsSchemaVersion); err != nil {
				t.Errorf("RequireControlsSchema as %s: %v", role, err)
			}
		})
	}
}
