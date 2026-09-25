package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaVersion is the v2 goose version this binary was built for. Roles
// pass it to RequireSchema so a pod never serves against an older schema.
const SchemaVersion = 2

// ErrSchemaTooOld is returned by RequireSchema when the database is
// behind the version the caller needs. Run `repo-guardian migrate`.
var ErrSchemaTooOld = errors.New("database schema is older than this binary; run `repo-guardian migrate`")

// RequireSchema fails unless goose_db_version records minVersion or
// later as applied. Roles call it for readiness (DESIGN-0025 OQ5):
// migrations run once per release in a hook Job, never per pod.
func RequireSchema(ctx context.Context, pool *pgxpool.Pool, minVersion int64) error {
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.`+VersionTable+`') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("postgres.RequireSchema: probe version table: %w", err)
	}

	if !exists {
		return fmt.Errorf("postgres.RequireSchema: no %s table: %w", VersionTable, ErrSchemaTooOld)
	}

	var current int64
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(max(version_id), 0) FROM `+VersionTable+` WHERE is_applied`).Scan(&current); err != nil {
		return fmt.Errorf("postgres.RequireSchema: read version: %w", err)
	}

	if current < minVersion {
		return fmt.Errorf("postgres.RequireSchema: at version %d, need %d: %w", current, minVersion, ErrSchemaTooOld)
	}

	return nil
}
