package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrationsControls holds the controls line's goose chain (DESIGN-0032
// § Data model, D27): a fresh chain for an empty database, never applied
// over the rc line's migrations_v2.
//
//go:embed migrations_controls
var migrationsControls embed.FS

const controlsDir = "migrations_controls"

// ControlsVersionTable is the controls chain's goose version table. It
// differs from the rc's goose_db_version, so the chain run against an rc
// database fails on its first CREATE TABLE instead of trusting the rc's
// version.
const ControlsVersionTable = "goose_controls_version"

// ControlsSchemaVersion is the controls chain version this binary was
// built for, tracked apart from the rc's SchemaVersion. IMPL-0029 and
// IMPL-0030 raise it as they add 00002 to 00004.
const ControlsSchemaVersion = 1

// NewControlsMigrator returns a goose provider over the controls chain,
// serialized by a session advisory lock like NewMigrator.
func NewControlsMigrator(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationsControls, controlsDir)
	if err != nil {
		return nil, fmt.Errorf("opening embedded controls migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("creating session locker: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys,
		goose.WithTableName(ControlsVersionTable),
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("creating controls migrator: %w", err)
	}

	return provider, nil
}

// ControlsDryRun applies every pending controls migration inside one
// transaction and rolls it back, returning the versions it applied. The
// chain is SQL only, so unlike the rc's DryRun there is no per-file
// wiring: a new file is rehearsed by being in the directory.
func ControlsDryRun(ctx context.Context, db *sql.DB) (_ []int64, retErr error) {
	files, err := controlsMigrations()
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("controls dry run: begin: %w", err)
	}

	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			retErr = errors.Join(retErr, fmt.Errorf("controls dry run: rollback: %w", err))
		}
	}()

	current, err := versionIn(ctx, tx, ControlsVersionTable)
	if err != nil {
		return nil, err
	}

	var applied []int64

	for _, f := range files {
		if f.version <= current {
			continue
		}

		if _, err := tx.ExecContext(ctx, f.up); err != nil {
			return nil, fmt.Errorf("controls dry run: %05d: %w", f.version, err)
		}

		applied = append(applied, f.version)
	}

	return applied, nil
}

type sqlMigration struct {
	version int64
	up      string
}

// controlsMigrations reads the chain's Up sections in version order.
func controlsMigrations() ([]sqlMigration, error) {
	entries, err := fs.ReadDir(migrationsControls, controlsDir)
	if err != nil {
		return nil, fmt.Errorf("reading controls migrations: %w", err)
	}

	var out []sqlMigration

	for _, e := range entries {
		if path.Ext(e.Name()) != ".sql" {
			continue
		}

		prefix, _, _ := strings.Cut(e.Name(), "_")

		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("controls migration %s: no version prefix: %w", e.Name(), err)
		}

		body, err := migrationsControls.ReadFile(path.Join(controlsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}

		up, err := upSection(string(body))
		if err != nil {
			return nil, fmt.Errorf("controls migration %s: %w", e.Name(), err)
		}

		out = append(out, sqlMigration{version: version, up: up})
	}

	return out, nil
}

// upSection is the text between `-- +goose Up` and `-- +goose Down`.
// The StatementBegin/End markers inside it are SQL comments, so the
// section runs as one multi-statement Exec.
func upSection(body string) (string, error) {
	_, rest, ok := strings.Cut(body, "-- +goose Up")
	if !ok {
		return "", errors.New("no -- +goose Up section")
	}

	up, _, _ := strings.Cut(rest, "-- +goose Down")

	return up, nil
}

// versionIn is the applied goose version recorded in table, 0 on a
// database goose has never touched.
func versionIn(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.`+table+`') IS NOT NULL`).Scan(&present); err != nil {
		return 0, fmt.Errorf("probing %s: %w", table, err)
	}

	if !present {
		return 0, nil
	}

	var v int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(version_id), 0) FROM `+table+` WHERE is_applied`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading %s: %w", table, err)
	}

	return v, nil
}
