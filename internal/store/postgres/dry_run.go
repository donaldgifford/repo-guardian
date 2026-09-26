package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// schemaMigrationFile is 00002, the only SQL migration a dry run replays.
const schemaMigrationFile = "migrations_v2/00002_v2_schema.sql"

// DryRun applies every pending v2 migration up to the backfill (00001
// adopt_v1, 00002 v2_schema, 00003 backfill_v1) inside one transaction,
// reports what the backfill would write, and rolls everything back
// (DESIGN-0025 OQ8). The database is left exactly as it was. A nil
// report means the backfill had already run.
func DryRun(ctx context.Context, db *sql.DB, seed string) (_ *BackfillReport, retErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dry run: begin: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			retErr = errors.Join(retErr, fmt.Errorf("dry run: rollback: %w", err))
		}
	}()

	version, err := gooseVersion(ctx, tx)
	if err != nil {
		return nil, err
	}

	if version >= 3 {
		return nil, nil //nolint:nilnil // nil report: nothing pending to rehearse
	}

	if version < 1 {
		if err := adoptV1Up(ctx, tx); err != nil {
			return nil, err
		}
	}

	if version < 2 {
		up, err := schemaUpSQL()
		if err != nil {
			return nil, err
		}

		if _, err := tx.ExecContext(ctx, up); err != nil {
			return nil, fmt.Errorf("dry run: 00002: %w", err)
		}
	}

	return BackfillV1(ctx, tx, seed)
}

// gooseVersion is the applied goose version, 0 on a database goose has
// never touched.
func gooseVersion(ctx context.Context, tx *sql.Tx) (int64, error) {
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.`+VersionTable+`') IS NOT NULL`).Scan(&present); err != nil {
		return 0, fmt.Errorf("dry run: probing %s: %w", VersionTable, err)
	}

	if !present {
		return 0, nil
	}

	var v int64
	if err := tx.QueryRowContext(ctx,
		`SELECT coalesce(max(version_id), 0) FROM `+VersionTable+` WHERE is_applied`).Scan(&v); err != nil {
		return 0, fmt.Errorf("dry run: reading %s: %w", VersionTable, err)
	}

	return v, nil
}

// schemaUpSQL returns 00002's Up section with the goose annotations
// removed, as one multi-statement script.
func schemaUpSQL() (string, error) {
	b, err := migrationsV2.ReadFile(schemaMigrationFile)
	if err != nil {
		return "", fmt.Errorf("dry run: reading 00002: %w", err)
	}

	up, _, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		return "", errors.New("dry run: 00002 has no Down section")
	}

	var sb strings.Builder

	for line := range strings.Lines(up) {
		if !strings.HasPrefix(strings.TrimSpace(line), "-- +goose") {
			sb.WriteString(line)
		}
	}

	return sb.String(), nil
}
