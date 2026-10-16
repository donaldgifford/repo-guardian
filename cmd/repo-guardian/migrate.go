package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

const cmdMigrate = "migrate"

// migrateOptions are the parsed `repo-guardian migrate` flags.
type migrateOptions struct {
	// chain is the goose chain to apply: chainRC (default) or
	// chainControls.
	chain     string
	dsn       string
	freshness time.Duration
	dryRun    bool
	json      bool

	// wait is how long to wait for the database to accept connections
	// before migrating (IMPL-0028 8.2); zero tries once.
	wait time.Duration

	// policy is the controls policy root activated after the controls
	// chain applies (IMPL-0029 4.5); empty skips activation.
	policy string

	// forceRunning skips the running-v1 guard. Test-only; hidden from
	// usage because an operator should scale v1 down instead.
	forceRunning bool
}

// The migrate chains. chainControls is the controls line's fresh chain
// for an empty database (IMPL-0028 Phase 6, DESIGN-0032 D27); no chart
// default selects it until IMPL-0029's switch-over.
const (
	chainRC       = "v2"
	chainControls = "controls"
)

// defaultMigrateWait is how long migrate waits for the database. It sits
// under the chart's 600s Job deadline so a database that never comes up
// fails with its last error, not a killed pod.
const defaultMigrateWait = 5 * time.Minute

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

	// Activation is the controls policy version activated, if any.
	Activation *activation `json:"activation,omitempty"`
}

// activation is the controls policy version a migrate run activated
// (DESIGN-0030 D14).
type activation struct {
	Version   string `json:"version"`
	FirstSeen bool   `json:"first_seen"`
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
	opts, err := parseMigrateFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	res, err := migrate(context.Background(), &opts)
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

	defWait := defaultMigrateWait
	if v := os.Getenv("MIGRATE_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return migrateOptions{}, fmt.Errorf("invalid MIGRATE_WAIT %q: %w", v, err)
		}

		defWait = d
	}

	fs.StringVar(&opts.chain, "chain", chainRC, "schema chain: v2 (the rc line) or controls (an empty database only)")
	fs.StringVar(&opts.dsn, "dsn", os.Getenv("STORE_DSN"), "Postgres DSN with DDL rights (defaults to $STORE_DSN)")
	fs.DurationVar(&opts.freshness, "freshness", defFreshness,
		"v1 freshness used to seed next check times (defaults to $RECONCILE_FRESHNESS, else 24h)")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "report what would be applied without changing the database")
	fs.BoolVar(&opts.json, "json", false, "print the result as JSON")
	fs.StringVar(&opts.policy, "policy", os.Getenv("GUARDIAN_CONFIG"),
		"controls policy root to activate after --chain controls applies (defaults to $GUARDIAN_CONFIG; empty skips)")
	fs.DurationVar(&opts.wait, "wait", defWait,
		"how long to wait for the database to accept connections (defaults to $MIGRATE_WAIT, else 5m; 0 tries once)")

	if err := fs.Parse(args); err != nil {
		return migrateOptions{}, err
	}

	if opts.dsn == "" {
		return migrateOptions{}, errors.New("no database given; pass --dsn or set STORE_DSN")
	}

	if opts.chain != chainRC && opts.chain != chainControls {
		return migrateOptions{}, fmt.Errorf("unknown --chain %q: want %s or %s", opts.chain, chainRC, chainControls)
	}

	return opts, nil
}

// migrate applies (or, with dryRun, lists) the pending v2 migrations.
func migrate(ctx context.Context, opts *migrateOptions) (_ *migrateResult, retErr error) {
	if opts.chain == chainControls {
		return migrateControls(ctx, opts)
	}

	db, err := openReady(ctx, opts)
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

// activateControlsPolicy loads the policy root, computes its version and
// activates it, as the owner (INV-0022 Phase-0 OQ1 (a)): activation is
// the migrate Job's and never a worker's, so workers read
// CurrentActivation and roll out what the deploy activated. Re-running
// for the version already current is a no-op (IMPL-0028 8.4).
func activateControlsPolicy(ctx context.Context, opts *migrateOptions) (*activation, error) {
	if opts.policy == "" {
		return nil, nil //nolint:nilnil // no policy root: nothing to activate
	}

	snap, err := policy.Load(opts.policy, builtin.Registry(), slog.Default())
	if err != nil {
		return nil, fmt.Errorf("loading policy %s: %w", opts.policy, err)
	}

	summary, err := json.Marshal(snap.Summarize())
	if err != nil {
		return nil, fmt.Errorf("summarizing policy: %w", err)
	}

	pool, err := pgxpool.New(ctx, opts.dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to activate the policy: %w", err)
	}
	defer pool.Close()

	firstSeen, err := pgstore.NewControlsStore(pool).ActivatePolicyVersion(ctx, snap.Version(), summary)
	if err != nil {
		return nil, err //nolint:wrapcheck // ActivatePolicyVersion names the version
	}

	return &activation{Version: snap.Version(), FirstSeen: firstSeen}, nil
}

// migrateControls applies (or, with dryRun, rehearses in a rolled-back
// transaction) the controls chain. It has no v1 guard and no backfill:
// the chain runs on an empty database.
func migrateControls(ctx context.Context, opts *migrateOptions) (_ *migrateResult, retErr error) {
	db, err := openReady(ctx, opts)
	if err != nil {
		return nil, err
	}

	provider, err := pgstore.NewControlsMigrator(db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}

	// Provider.Close closes db.
	defer func() {
		retErr = errors.Join(retErr, provider.Close())
	}()

	res := &migrateResult{DryRun: opts.dryRun}

	if opts.dryRun {
		if res.Pending, err = pgstore.ControlsDryRun(ctx, db); err != nil {
			return nil, err
		}
	} else if err := applyControls(ctx, provider, opts, res); err != nil {
		return nil, err
	}

	if res.Version, err = provider.GetDBVersion(ctx); err != nil {
		return nil, fmt.Errorf("reading schema version: %w", err)
	}

	return res, nil
}

// applyControls applies the pending controls migrations, then activates
// the policy.
func applyControls(ctx context.Context, provider *goose.Provider, opts *migrateOptions, res *migrateResult) error {
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("applying controls migrations: %w", err)
	}

	for _, r := range results {
		res.Applied = append(res.Applied, appliedMigration{
			Version: r.Source.Version, Path: r.Source.Path, Duration: r.Duration,
		})
	}

	res.Activation, err = activateControlsPolicy(ctx, opts)

	return err
}

// openReady opens the database and waits for it to accept connections:
// under Argo CD the migrate Job can start before Postgres does. Progress
// goes to stderr, keeping stdout for the result.
func openReady(ctx context.Context, opts *migrateOptions) (*sql.DB, error) {
	db, err := pgstore.OpenDB(opts.dsn)
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("command", cmdMigrate)
	if err := pgstore.WaitReady(ctx, db, opts.wait, logger); err != nil {
		return nil, errors.Join(err, db.Close())
	}

	return db, nil
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
