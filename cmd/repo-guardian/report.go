package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/report"
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
// running older server.
func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)

	out := fs.String("out", "./reports", "directory to write one markdown report per org into")
	dsn := fs.String("dsn", os.Getenv("STORE_DSN"), "Postgres DSN (defaults to $STORE_DSN)")
	withPRLinks := fs.Bool("with-pr-links", false,
		"resolve open repo-guardian PR links via the GitHub App (needs app credentials and network access)")

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
	logger := initLogger(os.Getenv("LOG_LEVEL"))

	st, err := pgstore.New(ctx, *dsn, reportPoolConns, logger)
	if err != nil {
		return fmt.Errorf("report: open store: %w", err)
	}

	defer func() {
		if err := st.Close(); err != nil {
			logger.Warn("report: store close failed", "error", err)
		}
	}()

	data, err := st.ReportData(ctx)
	if err != nil {
		return fmt.Errorf("report: read state: %w", err)
	}

	var linker report.PRLinker

	if *withPRLinks {
		linker, err = newPRLinker(logger)
		if err != nil {
			return err
		}
	}

	renderer, err := report.New(report.Options{Links: linker, Logger: logger})
	if err != nil {
		return err
	}

	orgs := renderer.Build(data)
	renderer.Enrich(ctx, data, orgs)

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

// newPRLinker builds the GitHub-backed link resolver.
//
// Constructs a minimal config rather than calling config.Load, for the
// reason given on runReport: the report needs App credentials and
// nothing else Load insists on.
func newPRLinker(logger *slog.Logger) (report.PRLinker, error) {
	appID, err := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("report: --with-pr-links needs a numeric GITHUB_APP_ID: %w", err)
	}

	client, err := newGitHubClient(&config.Config{
		GitHubAppID:          appID,
		GitHubPrivateKey:     os.Getenv("GITHUB_PRIVATE_KEY"),
		GitHubPrivateKeyPath: os.Getenv("GITHUB_PRIVATE_KEY_PATH"),
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("report: --with-pr-links: %w", err)
	}

	return report.NewGitHubLinker(report.GitHubLinkerOptions{
		Client: client,
		// Injected rather than imported inside internal/report: taking
		// the const from internal/checker would drag the engine, the
		// policy loader, the reconcilers and their metric registrations
		// into a read-only CLI.
		BranchName: checker.BranchName,
		Logger:     logger,
	}), nil
}
