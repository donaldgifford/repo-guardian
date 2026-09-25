package postgres

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	// Registers the "pgx" database/sql driver used by goose.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrationsV2 holds the goose SQL migrations. It is kept apart from
// v1's golang-migrate directory so neither tool ever reads the other's
// files.
//
//go:embed migrations_v2
var migrationsV2 embed.FS

// VersionTable is the goose version table. It is distinct from v1's
// schema_migrations, so a v1 binary started against a migrated database
// never sees v2's history.
const VersionTable = "goose_db_version"

// OpenDB opens a database/sql handle over pgx for goose. The caller
// owns the returned handle and must close it.
func OpenDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	return db, nil
}

// NewMigrator returns a goose provider over the embedded v2 migrations.
// A Postgres session-level advisory lock serializes concurrent runners,
// so two migrate Jobs racing on one database apply each migration once.
func NewMigrator(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationsV2, "migrations_v2")
	if err != nil {
		return nil, fmt.Errorf("opening embedded migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("creating session locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(VersionTable),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(goMigrations()...),
	)
	if err != nil {
		return nil, fmt.Errorf("creating migrator: %w", err)
	}

	return provider, nil
}

// goMigrations lists the Go migrations, in version order. They live in
// code rather than in migrations_v2 because their logic branches.
func goMigrations() []*goose.Migration {
	return []*goose.Migration{
		adoptV1Migration(),
	}
}
