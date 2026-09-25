package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/ingest"
	"github.com/donaldgifford/repo-guardian/internal/observability"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// Role subcommands (DESIGN-0026 § Roles). One binary, one image; the
// role is the first argument, and no argument means all.
const (
	cmdIngest = "ingest"
	cmdWorker = "worker"
	cmdAPI    = "api"
	cmdAll    = "all"

	// cmdV1 runs the v1 server until the v1 runtime is deleted
	// (IMPL-0025 Phase 16). Hidden from usage.
	cmdV1 = "v1"
)

// errAPINotImplemented is the api role's answer until Phase 14.
var errAPINotImplemented = errors.New("the api role is not implemented yet (IMPL-0025 Phase 14)")

func runIngest(args []string) error { return runRoles(cmdIngest, args, config.RoleIngest) }

func runWorker(args []string) error { return runRoles(cmdWorker, args, config.RoleWorker) }

// runAll runs every role in one process. The api role is skipped until
// it exists.
func runAll(args []string) error {
	return runRoles(cmdAll, args, config.RoleIngest|config.RoleWorker)
}

// runAPI is the read-only API role, a stub until Phase 14.
func runAPI([]string) error { return errAPINotImplemented }

// runRoles brings up the given roles in one process, serves until a
// signal, then shuts down.
func runRoles(name string, args []string, roles config.Role) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	strictTemplates := fs.Bool("strict-templates", strictTemplatesFromEnv(),
		"Validate every compiled PR template against a zero-value PRVars context at startup; exit non-zero on failure")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadRole(roles)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := initLogger(cfg.LogLevel).With("role", name)
	slog.SetDefault(logger)
	warnRemovedEnvVars(logger)

	obs, err := observability.New(observability.Options{Logger: logger})
	if err != nil {
		return fmt.Errorf("bootstrap observability: %w", err)
	}

	defer shutdownObservability(logger, obs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tcfg, err := temporal.ConfigFromEnv()
	if err != nil {
		return err
	}

	tc, err := temporal.Dial(ctx, &tcfg, temporal.DialOptions{Logger: logger, MeterProvider: obs.MeterProvider})
	if err != nil {
		return err
	}
	defer tc.Close()

	if err := temporal.CheckServerVersion(ctx, tc, temporal.MinServerVersion); err != nil {
		return err
	}

	mux, w, err := bringUpRoles(ctx, roles, cfg, tc, &tcfg, *strictTemplates, logger)
	if err != nil {
		return err
	}

	mainServer := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	metricsServer := newMetricsServer(cfg.MetricsAddr)

	startServer(logger, mainServer, "main", cfg.ListenAddr, cancel)
	startServer(logger, metricsServer, "metrics", cfg.MetricsAddr, cancel)

	awaitShutdown(ctx, logger)
	cancel()
	stopRoles(logger, w, mainServer, metricsServer)

	return nil
}

// bringUpRoles starts the worker and mounts ingest and the health
// endpoints for the given roles.
func bringUpRoles(
	ctx context.Context,
	roles config.Role,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (*http.ServeMux, worker.Worker, error) {
	checks := []readinessCheck{{name: "temporal", fn: func(ctx context.Context) error { return temporal.Ping(ctx, tc) }}}

	mux := http.NewServeMux()

	var w worker.Worker

	if roles.Has(config.RoleWorker) {
		var (
			pool *pgxpool.Pool
			err  error
		)

		w, pool, err = startV2Worker(ctx, cfg, tc, tcfg, strictTemplates, logger)
		if err != nil {
			return nil, nil, err
		}

		checks = append(checks, readinessCheck{name: "schema", fn: func(ctx context.Context) error {
			return pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion)
		}})
	}

	if roles.Has(config.RoleIngest) {
		h, err := newIngestHandler(cfg, tc, tcfg.TaskQueue, logger)
		if err != nil {
			return nil, nil, err
		}

		mux.Handle(webhookRoute, observability.Handler(h, webhookRoute))
	}

	ready := newReadiness(logger, checks...)
	go ready.run(ctx, readinessInterval)

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", ready.handler(ctx))

	return mux, w, nil
}

// startV2Worker loads the policy and engine, opens the store and starts
// a Temporal worker running every workflow and activity. It returns the
// store pool for the readiness check.
func startV2Worker(
	ctx context.Context,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (worker.Worker, *pgxpool.Pool, error) {
	policyCfg, engine, templates := loadPolicyAndEngine(cfg, strictTemplates, logger)

	policyVersion, err := policy.VersionV2(policyCfg, templates.AsMap())
	if err != nil {
		return nil, nil, fmt.Errorf("policy version: %w", err)
	}

	gh, err := newGitHubClient(cfg, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("create github client: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.StoreDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open store: %w", err)
	}

	if err := pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion); err != nil {
		pool.Close()

		return nil, nil, err
	}

	wc, err := temporal.WorkerConfigFromEnv(tcfg)
	if err != nil {
		pool.Close()

		return nil, nil, err
	}

	st := pgstore.NewV2Store(pool, logger, pgstore.WithHost(cfg.GitHubHost))

	w := temporal.NewWorker(tc, &wc)
	workflows.Register(w)
	activities.New(engine, st, gh, policyVersion, logger).Register(w)
	activities.NewBudget(tc, wc.TaskQueue, cfg.RateLimitThreshold).Register(w)
	activities.NewRouter(st, tc, wc.TaskQueue, cfg.ReconcileFreshness, cfg.RateLimitThreshold, logger).Register(w)

	if err := w.Start(); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("start temporal worker: %w", err)
	}

	logger.Info("temporal worker started",
		"task_queue", wc.TaskQueue, "build_id", wc.BuildID,
		"activity_concurrency", wc.ActivityConcurrency, "policy_version", policyVersion)

	// The pool lives as long as the process; the worker is stopped first
	// on shutdown, so no activity is left holding a connection.
	go func() {
		<-ctx.Done()
		w.Stop()
		pool.Close()
	}()

	return w, pool, nil
}

// newIngestHandler builds the webhook handler. The policy is read only
// for its watched paths, so a policy error fails startup rather than
// silently dropping every push.
func newIngestHandler(cfg *config.Config, tc client.Client, taskQueue string, logger *slog.Logger) (http.Handler, error) {
	policyCfg, err := policy.Load(cfg.GuardianConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load policy for watched paths: %w", err)
	}

	return ingest.New(cfg.GitHubWebhookSecret, tc, taskQueue, policy.ExtractWatchedPaths(policyCfg), logger), nil
}

// stopRoles shuts the HTTP servers down. The worker stops with the run
// context.
func stopRoles(logger *slog.Logger, _ worker.Worker, servers ...*http.Server) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "addr", srv.Addr, "error", err)
		}
	}

	logger.Info("repo-guardian stopped")
}

func shutdownObservability(logger *slog.Logger, obs *observability.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), observabilityShutdownTimeout)
	defer cancel()

	if err := obs.Shutdown(ctx); err != nil {
		logger.Warn("observability shutdown error", "error", err)
	}
}
