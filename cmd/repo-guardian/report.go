package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/report"
	"github.com/donaldgifford/repo-guardian/internal/store"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// reportPoolConns is the pool size for a one-shot CLI run. The report
// issues one read; the server's STORE_POSTGRES_MAX_CONNS is sized for a
// worker pool and would be rude to a shared database from a laptop.
const reportPoolConns = 2

// runReport implements `repo-guardian report`.
//
// Deliberately does NOT call config.Load(). Load validates the whole
// server configuration — App ID, private key, webhook secret, and a
// Valkey DSN, because QUEUE_BACKEND defaults to valkey — and a read-only
// report needs none of it. An operator running this from a laptop would
// otherwise be told to set a webhook secret for a command that never
// serves a webhook.
//
// It also does NOT run migrations. The report is read-only, the DSN it
// is handed may have no DDL rights, and a report generated from a newer
// binary would otherwise migrate the schema forward underneath a
// running older server. It checks the schema version instead and
// refuses an unmigrated database (run `repo-guardian migrate`).
func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)

	out := fs.String("out", "./reports", "directory to write one markdown report per org into")
	dsn := fs.String("dsn", os.Getenv("STORE_DSN"), "Postgres DSN (defaults to $STORE_DSN)")
	withPRLinks := fs.Bool("with-pr-links", false,
		"DEPRECATED no-op: PR links now come from recorded evidence and are always shown; removed in the next release")
	scope := fs.String("orgs", "", "comma-separated orgs to report on (default: every org)")

	if err := fs.Parse(args); err != nil {
		// -h is a request, not a failure. Without this the binary
		// prints usage and then logs "exited with error" over it.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("report: %w", err)
	}

	if *dsn == "" {
		return errors.New("report: no database given; pass --dsn or set STORE_DSN")
	}

	ctx := context.Background()

	// Stderr, not the server's stdout. The path list below is this
	// command's output and has to be pipeable; JSON log lines
	// interleaved into it would make `report | xargs` parse a log
	// record as a filename.
	logger := initLoggerTo(os.Stderr, os.Getenv("LOG_LEVEL"))

	if *withPRLinks {
		logger.Warn("report: --with-pr-links is deprecated and does nothing; PR links come from recorded evidence")
	}

	pool, err := newReportPool(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion); err != nil {
		return fmt.Errorf("report: %w", err)
	}

	data, err := pgstore.NewV2Store(pool, logger).ComplianceReport(ctx, parseScope(*scope))
	if err != nil {
		return fmt.Errorf("report: read state: %w", err)
	}

	renderer, err := report.New(report.Options{Logger: logger})
	if err != nil {
		return err
	}

	orgs := renderer.Build(data)

	paths, err := renderer.WriteAll(*out, orgs)
	if err != nil {
		return err
	}

	logger.Info("compliance reports written", "count", len(paths), "dir", *out)

	// Printed so a shell pipeline can consume the list; the logger writes
	// to stderr, so stdout carries nothing but paths.
	for _, p := range paths {
		if _, err := fmt.Fprintln(os.Stdout, p); err != nil {
			return fmt.Errorf("list written reports: %w", err)
		}
	}

	return nil
}

// newReportPool opens a small read pool for a one-shot CLI run.
func newReportPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("report: parse dsn: %w", err)
	}

	cfg.MaxConns = reportPoolConns

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("report: open store: %w", err)
	}

	return pool, nil
}

// parseScope turns --orgs into a store.Scope; empty means every org.
func parseScope(orgs string) store.Scope {
	var scope store.Scope

	for o := range strings.SplitSeq(orgs, ",") {
		if o = strings.TrimSpace(o); o != "" {
			scope.Orgs = append(scope.Orgs, o)
		}
	}

	return scope
}
