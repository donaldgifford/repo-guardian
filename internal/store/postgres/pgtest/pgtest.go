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

// The controls roles (DESIGN-0032 D8, D31). Each logs in with its name
// as its password; the chart's init script and the external-mode SQL in
// docs/operations/v2-onboarding.md create the same roles.
const (
	// OwnerRole owns the schema and runs the controls chain. It holds no
	// CREATEROLE: in baked mode the image's POSTGRES_USER is a superuser,
	// so the migrate Job connects as this role instead (INV-0022 spike 6).
	OwnerRole = "rg_owner"
	// EvaluatorRole and RemediatorRole are the two application roles.
	EvaluatorRole  = "rg_evaluator"
	RemediatorRole = "rg_remediator"
	// AllRole is the `all` topology's role: a member of both.
	AllRole = "rg_all"
)

// ControlsDSNs connects as each controls role.
type ControlsDSNs struct {
	Owner      string
	Evaluator  string
	Remediator string
	All        string
}

// ControlsRoles provisions the controls roles the way the chart and the
// external-database docs do, then returns a DSN for each. Migrations
// grant to the application roles and never create them, so this runs
// before any migration, as the admin in dsn. Keep the statements in step
// with the chart's store-postgres-roles init script.
func ControlsRoles(tb testing.TB, dsn string) ControlsDSNs {
	tb.Helper()

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		tb.Fatalf("pgtest: connect: %v", err)
	}

	defer conn.Close(ctx) //nolint:errcheck // test cleanup; nothing to recover

	for _, stmt := range []string{
		"CREATE ROLE " + OwnerRole + " LOGIN PASSWORD '" + OwnerRole + "' NOSUPERUSER NOCREATEROLE",
		"GRANT ALL ON SCHEMA public TO " + OwnerRole,
		"CREATE ROLE " + EvaluatorRole + " LOGIN PASSWORD '" + EvaluatorRole + "' NOSUPERUSER",
		"CREATE ROLE " + RemediatorRole + " LOGIN PASSWORD '" + RemediatorRole + "' NOSUPERUSER",
		"CREATE ROLE " + AllRole + " LOGIN PASSWORD '" + AllRole + "' NOSUPERUSER IN ROLE " + EvaluatorRole + ", " + RemediatorRole,
		"GRANT USAGE ON SCHEMA public TO " + EvaluatorRole + ", " + RemediatorRole,
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

	return ControlsDSNs{Owner: as(OwnerRole), Evaluator: as(EvaluatorRole), Remediator: as(RemediatorRole), All: as(AllRole)}
}

// ControlsDB starts a fresh database, provisions the controls roles and
// applies the whole controls chain as the owner. Tests then connect as
// an application role, never as the owner: an owner cannot fail on a
// missing grant.
func ControlsDB(tb testing.TB) ControlsDSNs {
	tb.Helper()

	dsns := ControlsRoles(tb, Start(tb))

	db, err := postgres.OpenDB(dsns.Owner)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	provider, err := postgres.NewControlsMigrator(db)
	if err != nil {
		tb.Fatalf("pgtest: %v", err)
	}

	defer provider.Close() //nolint:errcheck // closes db; nothing to recover

	if _, err := provider.Up(context.Background()); err != nil {
		tb.Fatalf("pgtest: apply controls chain: %v", err)
	}

	return dsns
}
