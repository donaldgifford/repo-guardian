package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
)

// v1SchemaVersion is the last golang-migrate version v1 shipped. v2
// adopts only a v1 database that is exactly here and clean.
const v1SchemaVersion = 3

// MetaAdoptedV1At is the v2_meta key recording that migrate adopted a
// v1 database. The backfill runs only when it is present.
const MetaAdoptedV1At = "adopted_v1_at"

// ErrV1SchemaNotAdoptable is returned when a v1 schema is present but
// is not at the last v1 version, or is dirty.
var ErrV1SchemaNotAdoptable = errors.New(
	"v1 schema is not adoptable: upgrade to the last v1.x first, or resolve the dirty migration")

// adoptV1Migration is 00001_adopt_v1. It creates v2_meta and, when a
// v1 schema is present, records that it was adopted. A fresh database
// is a no-op apart from v2_meta; any other v1 state fails the run
// before a single v2 table exists.
func adoptV1Migration() *goose.Migration {
	return goose.NewGoMigration(1,
		&goose.GoFunc{RunTx: adoptV1Up},
		&goose.GoFunc{RunTx: adoptV1Down},
	)
}

func adoptV1Up(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS v2_meta (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("creating v2_meta: %w", err)
	}

	var present bool
	if err := tx.QueryRowContext(ctx,
		`SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&present); err != nil {
		return fmt.Errorf("probing for a v1 schema: %w", err)
	}

	if !present {
		return nil
	}

	var (
		version int64
		dirty   bool
	)

	err := tx.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w (schema_migrations is empty)", ErrV1SchemaNotAdoptable)
	}

	if err != nil {
		return fmt.Errorf("reading v1 schema version: %w", err)
	}

	if version != v1SchemaVersion || dirty {
		return fmt.Errorf("%w (found version %d, dirty=%t; want %d, clean)",
			ErrV1SchemaNotAdoptable, version, dirty, v1SchemaVersion)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO v2_meta (key, value) VALUES ($1, now()::text) ON CONFLICT (key) DO NOTHING`,
		MetaAdoptedV1At); err != nil {
		return fmt.Errorf("recording v1 adoption: %w", err)
	}

	return nil
}

func adoptV1Down(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS v2_meta`); err != nil {
		return fmt.Errorf("dropping v2_meta: %w", err)
	}

	return nil
}

// v1IdleWindow is how recently a v1 replica may have written
// repo_state before migrate refuses to run.
const v1IdleWindow = 60 * time.Second

// ErrV1Running is returned by CheckV1Idle when repo_state was written
// within the idle window, i.e. a v1 replica is probably still running.
var ErrV1Running = errors.New("v1 appears to be running; scale it to zero first")

// CheckV1Idle refuses when repo_state exists and a row was checked in
// the last minute. It is a cheap guard against migrating under a live
// v1, not a lock: the runbook scales v1 to zero first.
func CheckV1Idle(ctx context.Context, db *sql.DB) error {
	var running bool

	err := db.QueryRowContext(ctx, `
		SELECT to_regclass('public.repo_state') IS NOT NULL
		   AND EXISTS (SELECT 1 FROM repo_state WHERE last_checked_at > now() - make_interval(secs => $1))`,
		v1IdleWindow.Seconds()).Scan(&running)
	if err != nil {
		return fmt.Errorf("checking for a running v1: %w", err)
	}

	if running {
		return ErrV1Running
	}

	return nil
}
