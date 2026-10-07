// Package pgtest provisions throwaway Postgres databases for the store's
// integration tests: one container per test, plus helpers that put the
// database into a known v1 or v2 state. It is imported only from tests
// behind the `integration` build tag.
package pgtest

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// Image is the Postgres image tests run against: the same major as the
// chart's baked StatefulSet.
const Image = "postgres:18.4-alpine"

// Start runs a fresh Postgres container for tb and returns its DSN. The
// container is terminated when tb finishes.
func Start(tb testing.TB) string {
	tb.Helper()

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("repoguardian_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			// The server logs "ready" twice: once for the init run and
			// once for the real start. Waiting for the second avoids
			// connecting to the init-only instance.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		tb.Fatalf("pgtest: start postgres: %v", err)
	}

	tb.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			tb.Logf("pgtest: terminate postgres: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		tb.Fatalf("pgtest: connection string: %v", err)
	}

	return dsn
}

// SeedV1 brings dsn to v1's final schema (golang-migrate version 3)
// using v1's own embedded migrations, exactly as a v1 pod would at
// startup. It is the starting point for every adoption and backfill
// test.
func SeedV1(tb testing.TB, dsn string) {
	tb.Helper()

	if err := postgres.Migrate(dsn); err != nil {
		tb.Fatalf("pgtest: seed v1 schema: %v", err)
	}
}

// AppRole creates a non-superuser login role that owns the public schema
// and returns dsn rewritten to connect as it. Superusers bypass grants,
// so tests that assert a grant (finding_events is append-only) must
// migrate and write as this role, the way the application does.
func AppRole(tb testing.TB, dsn string) string {
	tb.Helper()

	const role, password = "rg_app", "rg_app"

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgtest: connect: %v", err)
	}

	defer conn.Close(ctx) //nolint:errcheck // test cleanup; nothing to recover

	for _, stmt := range []string{
		"CREATE ROLE " + role + " LOGIN PASSWORD '" + password + "' NOSUPERUSER",
		"GRANT ALL ON SCHEMA public TO " + role,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			tb.Fatalf("pgtest: %s: %v", stmt, err)
		}
	}

	u, err := url.Parse(dsn)
	if err != nil {
		tb.Fatalf("pgtest: parse dsn: %v", err)
	}

	u.User = url.UserPassword(role, password)

	return u.String()
}

// ControlsDSNs connects as each role the controls schema expects
// (DESIGN-0032 D8, D31).
type ControlsDSNs struct {
	// Owner owns the schema and runs migrations; it holds no CREATEROLE.
	Owner string
	// Evaluator and Remediator are the two application roles.
	Evaluator  string
	Remediator string
	// All is the `all` topology's role: a member of both.
	All string
}

// ControlsRoles provisions the controls roles the way the chart and the
// external-database docs do, then returns a DSN for each. Migrations
// grant to the application roles and never create them, so this runs
// before any migration, as the admin in dsn.
//
// NOTE: IMPL-0028 task 0.11 prototype. Phase 6 keeps the statements in
// step with the chart's init script and the external-mode SQL.
func ControlsRoles(tb testing.TB, dsn string) ControlsDSNs {
	tb.Helper()

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgtest: connect: %v", err)
	}

	defer conn.Close(ctx) //nolint:errcheck // test cleanup; nothing to recover

	for _, stmt := range []string{
		"CREATE ROLE rg_owner LOGIN PASSWORD 'rg_owner' NOSUPERUSER NOCREATEROLE",
		"GRANT ALL ON SCHEMA public TO rg_owner",
		"CREATE ROLE rg_evaluator LOGIN PASSWORD 'rg_evaluator' NOSUPERUSER",
		"CREATE ROLE rg_remediator LOGIN PASSWORD 'rg_remediator' NOSUPERUSER",
		"CREATE ROLE rg_all LOGIN PASSWORD 'rg_all' NOSUPERUSER IN ROLE rg_evaluator, rg_remediator",
		"GRANT USAGE ON SCHEMA public TO rg_evaluator, rg_remediator",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			tb.Fatalf("pgtest: %s: %v", stmt, err)
		}
	}

	as := func(role string) string {
		u, err := url.Parse(dsn)
		if err != nil {
			tb.Fatalf("pgtest: parse dsn: %v", err)
		}

		u.User = url.UserPassword(role, role)

		return u.String()
	}

	return ControlsDSNs{Owner: as("rg_owner"), Evaluator: as("rg_evaluator"), Remediator: as("rg_remediator"), All: as("rg_all")}
}
