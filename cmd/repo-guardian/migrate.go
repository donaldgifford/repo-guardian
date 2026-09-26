package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"

	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

const cmdMigrate = "migrate"

// migrateOptions are the parsed `repo-guardian migrate` flags.
type migrateOptions struct {
	dsn       string
	freshness time.Duration
	dryRun    bool
	json      bool

	// forceRunning skips the running-v1 guard. Test-only; hidden from
	// usage because an operator should scale v1 down instead.
	forceRunning bool
}

// forceRunningFlag is parsed by hand so it never appears in -h output.
const forceRunningFlag = "--force-running"

// migrateResult is what a migrate run reports, as text or --json.
type migrateResult struct {
	DryRun  bool               `json:"dry_run"`
	Version int64              `json:"version"`
	Applied []appliedMigration `json:"applied"`
	Pending []int64            `json:"pending,omitempty"`

	// Backfill is what the v1 backfill would write, under --dry-run.
	Backfill *pgstore.BackfillReport `json:"backfill,omitempty"`
}

type appliedMigration struct {
	Version  int64         `json:"version"`
	Path     string        `json:"path,omitempty"`
	Duration time.Duration `json:"duration_ns"`
}

// runMigrate implements `repo-guardian migrate`: apply the embedded v2
// goose migrations once per release (the chart runs it as a Helm hook
// Job), then exit. Like `report`, it does not call config.Load(); a DSN
// is all it needs.
func runMigrate(args []string) error {
	if len(args) > 0 && args[0] == cmdVerifyShadow {
		return runVerifyShadow(args[1:])
	}

	opts, err := parseMigrateFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	res, err := migrate(context.Background(), opts)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return writeMigrateResult(os.Stdout, res, opts.json)
}

func parseMigrateFlags(args []string) (migrateOptions, error) {
	fs := flag.NewFlagSet(cmdMigrate, flag.ContinueOnError)

	var opts migrateOptions

	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool {
		if a == forceRunningFlag {
			opts.forceRunning = true

			return true
		}

		return false
	})

	defFreshness := pgstore.DefaultBackfillFreshness
	if v := os.Getenv("RECONCILE_FRESHNESS"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return migrateOptions{}, fmt.Errorf("invalid RECONCILE_FRESHNESS %q: %w", v, err)
		}

		defFreshness = d
	}

	fs.StringVar(&opts.dsn, "dsn", os.Getenv("STORE_DSN"), "Postgres DSN with DDL rights (defaults to $STORE_DSN)")
	fs.DurationVar(&opts.freshness, "freshness", defFreshness,
		"v1 freshness used to seed next check times (defaults to $RECONCILE_FRESHNESS, else 24h)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "report what would be applied without changing the database")
	fs.BoolVar(&opts.json, "json", false, "print the result as JSON")

	if err := fs.Parse(args); err != nil {
		return migrateOptions{}, err
	}

	if opts.dsn == "" {
		return migrateOptions{}, errors.New("no database given; pass --dsn or set STORE_DSN")
	}

	return opts, nil
}

// migrate applies (or, with dryRun, lists) the pending v2 migrations.
func migrate(ctx context.Context, opts migrateOptions) (_ *migrateResult, retErr error) {
	db, err := pgstore.OpenDB(opts.dsn)
	if err != nil {
		return nil, err
	}

	provider, err := pgstore.NewMigrator(db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}

	// Provider.Close closes db.
	defer func() {
		retErr = errors.Join(retErr, provider.Close())
	}()

	if !opts.forceRunning {
		if err := pgstore.CheckV1Idle(ctx, db); err != nil {
			return nil, err
		}
	}

	ctx = pgstore.WithBackfillFreshness(ctx, opts.freshness)
	res := &migrateResult{DryRun: opts.dryRun}

	if opts.dryRun {
		if err := rehearse(ctx, provider, db, res); err != nil {
			return nil, err
		}
	} else {
		results, err := provider.Up(ctx)
		if err != nil {
			return nil, fmt.Errorf("applying migrations: %w", err)
		}

		for _, r := range results {
			res.Applied = append(res.Applied, appliedMigration{
				Version: r.Source.Version, Path: r.Source.Path, Duration: r.Duration,
			})
		}
	}

	if res.Version, err = provider.GetDBVersion(ctx); err != nil {
		return nil, fmt.Errorf("reading schema version: %w", err)
	}

	return res, nil
}

// rehearse lists the pending migrations and runs them, with the v1
// backfill, in a transaction that rolls back.
func rehearse(ctx context.Context, provider *goose.Provider, db *sql.DB, res *migrateResult) error {
	if err := listPending(ctx, provider, res); err != nil {
		return err
	}

	backfill, err := pgstore.DryRun(ctx, db, strconv.FormatInt(time.Now().UnixNano(), 10))
	if err != nil {
		return err
	}

	res.Backfill = backfill

	return nil
}

func listPending(ctx context.Context, provider *goose.Provider, res *migrateResult) error {
	statuses, err := provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("reading migration status: %w", err)
	}

	for _, st := range statuses {
		if st.State == goose.StatePending {
			res.Pending = append(res.Pending, st.Source.Version)
		}
	}

	return nil
}

func writeMigrateResult(w io.Writer, res *migrateResult, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(res); err != nil {
			return fmt.Errorf("migrate: writing result: %w", err)
		}

		return nil
	}

	for _, a := range res.Applied {
		if _, err := fmt.Fprintf(w, "applied %05d %s (%s)\n", a.Version, a.Path, a.Duration.Round(time.Millisecond)); err != nil {
			return fmt.Errorf("migrate: writing result: %w", err)
		}
	}

	for _, v := range res.Pending {
		if _, err := fmt.Fprintf(w, "pending %05d\n", v); err != nil {
			return fmt.Errorf("migrate: writing result: %w", err)
		}
	}

	if _, err := fmt.Fprintf(w, "schema at version %d\n", res.Version); err != nil {
		return fmt.Errorf("migrate: writing result: %w", err)
	}

	if err := writeBackfillReport(w, res.Backfill); err != nil {
		return fmt.Errorf("migrate: writing result: %w", err)
	}

	return nil
}

// writeBackfillReport prints a dry run's backfill counts, collisions,
// multi-owner installations and park-reason histogram.
func writeBackfillReport(w io.Writer, b *pgstore.BackfillReport) error {
	if b == nil {
		return nil
	}

	if !b.Ran {
		_, err := fmt.Fprintln(w, "backfill: nothing to backfill (no v1 database adopted)")

		return err
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "backfill (dry run, rolled back; seed %s):\n", b.Seed)
	fmt.Fprintf(&sb, "  installations %d\n  repositories  %d (never checked %d)\n", b.Installations, b.Repositories, b.NeverChecked)
	fmt.Fprintf(&sb, "  findings      %d\n  events        %d\n  snapshots     %d\n", b.Findings, b.Events, b.Snapshots)

	for _, reason := range slices.Sorted(maps.Keys(b.ParkReasons)) {
		fmt.Fprintf(&sb, "  parked %-20s %d\n", reason, b.ParkReasons[reason])
	}

	for _, m := range b.MultiOwner {
		fmt.Fprintf(&sb, "  multiple owners: installation %s\n", m)
	}

	for _, c := range b.Collisions {
		fmt.Fprintf(&sb, "  case collision dropped: %s\n", c)
	}

	_, err := io.WriteString(w, sb.String())

	return err
}
