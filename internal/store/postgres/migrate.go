package postgres

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending Up migrations against the given DSN.
// Idempotent: returns nil if the schema is already at the latest
// version. The binary fails startup if Migrate returns a non-nil
// error other than ErrNoChange.
//
// Uses the golang-migrate `pgx/v5` database driver (matches the
// connection driver used by the Store's pool) and `iofs` source
// driver against the embedded migrations directory, so the binary
// has no filesystem dependency for migrations at runtime.
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("postgres.Migrate: open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+stripScheme(dsn))
	if err != nil {
		return fmt.Errorf("postgres.Migrate: open migrate: %w", err)
	}

	defer func() {
		_, _ = m.Close() //nolint:errcheck // deferred best-effort cleanup; both returns are tracked separately
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("postgres.Migrate: apply: %w", err)
	}

	return nil
}

// stripScheme accepts either a `postgres://` or `postgresql://`
// DSN and returns it without the scheme prefix, so it can be
// re-prefixed with the migrate-driver scheme (`pgx5://`).
func stripScheme(dsn string) string {
	for _, prefix := range []string{"postgres://", "postgresql://", "pgx5://"} {
		if len(dsn) >= len(prefix) && dsn[:len(prefix)] == prefix {
			return dsn[len(prefix):]
		}
	}

	return dsn
}

// migrationDriverImported is unused at runtime but ensures the pgx/v5
// migrate driver is linked into the binary (init() registers it
// against migrate.Register).
var _ = pgx.Postgres{} //nolint:exhaustruct // purely a registration marker
