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
	"os"
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
	// chain is the goose chain to apply; chainControls is the only one.
	chain  string
	dsn    string
	dryRun bool
	json   bool

	// wait is how long to wait for the database to accept connections
	// before migrating (IMPL-0028 8.2); zero tries once.
	wait time.Duration

	// policy is the controls policy root activated after the controls
	// chain applies (IMPL-0029 4.5); empty skips activation.
	policy string
}

// chainControls is the controls line's chain for an empty database
// (IMPL-0028 Phase 6, DESIGN-0032 D27), and since IMPL-0029 7.5 the only
// one: --chain stays so the chart's `--chain controls` keeps working.
const chainControls = "controls"

// defaultMigrateWait is how long migrate waits for the database. It sits
// under the chart's 600s Job deadline so a database that never comes up
// fails with its last error, not a killed pod.
const defaultMigrateWait = 5 * time.Minute

// migrateResult is what a migrate run reports, as text or --json.
type migrateResult struct {
	DryRun  bool               `json:"dry_run"`
	Version int64              `json:"version"`
	Applied []appliedMigration `json:"applied"`
	Pending []int64            `json:"pending,omitempty"`

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

// runMigrate implements `repo-guardian migrate`: apply the embedded
// controls chain once per release (the chart runs it as a Helm hook
// Job), activate the policy, then exit. Like `report`, it does not call config.Load(); a DSN
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

	defWait := defaultMigrateWait
	if v := os.Getenv("MIGRATE_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return migrateOptions{}, fmt.Errorf("invalid MIGRATE_WAIT %q: %w", v, err)
		}

		defWait = d
	}

	fs.StringVar(&opts.chain, "chain", chainControls, "schema chain: controls, the only one")
	fs.StringVar(&opts.dsn, "dsn", os.Getenv("STORE_DSN"), "Postgres DSN with DDL rights (defaults to $STORE_DSN)")
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

	if opts.chain != chainControls {
		return migrateOptions{}, fmt.Errorf("unknown --chain %q: want %s (the rc chain was removed in IMPL-0029)", opts.chain, chainControls)
	}

	return opts, nil
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

// migrate applies (or, with dryRun, rehearses in a rolled-back
// transaction) the controls chain. The chain runs on an empty database.
func migrate(ctx context.Context, opts *migrateOptions) (_ *migrateResult, retErr error) {
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

	return nil
}
