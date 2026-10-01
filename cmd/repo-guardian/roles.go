package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/api"
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

func runIngest(args []string) error { return runRoles(cmdIngest, args, config.RoleIngest) }

func runWorker(args []string) error { return runRoles(cmdWorker, args, config.RoleWorker) }

// runAll runs every role in one process; the API gets its own listener.
func runAll(args []string) error { return runRoles(cmdAll, args, config.RoleAll) }

// runAPI is the read-only API role (DESIGN-0027).
func runAPI(args []string) error { return runRoles(cmdAPI, args, config.RoleAPI) }

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

	obs, err := observability.New(observability.Options{Logger: logger, Views: temporal.MetricViews()})
	if err != nil {
		return fmt.Errorf("bootstrap observability: %w", err)
	}

	defer shutdownObservability(logger, obs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tcfg, tc, stopReload, err := dialTemporal(ctx, roles, obs, logger)
	if err != nil {
		return err
	}

	// Deferred first so it runs last: the poller outlives the client.
	defer stopReload()

	if tc != nil {
		defer tc.Close()
	}

	up, err := bringUpRoles(ctx, roles, cfg, tc, &tcfg, *strictTemplates, logger)
	if err != nil {
		return err
	}

	servers := listen(logger, cfg, roles, up, cancel)

	awaitShutdown(ctx, logger)
	cancel()
	stopRoles(logger, up.worker, servers...)

	return nil
}

// dialTemporal connects to Temporal for the roles that use it. The api
// role alone does not: it reads Postgres only. The returned stop func
// ends the TLS reload poller and is never nil.
func dialTemporal(
	ctx context.Context, roles config.Role, obs *observability.Provider, logger *slog.Logger,
) (tcfg temporal.Config, tc client.Client, stop func(), err error) {
	noop := func() {}

	tcfg, err = temporal.ConfigFromEnv()
	if roles == config.RoleAPI {
		// The api role never dials, so an unset or incomplete Temporal
		// env is not an error for it: the chart gives it none.
		return tcfg, nil, noop, nil
	}

	if err != nil {
		return tcfg, nil, noop, err
	}

	tc, stop, err = temporal.Dial(ctx, &tcfg, temporal.DialOptions{Logger: logger, MeterProvider: obs.MeterProvider})
	if err != nil {
		return tcfg, nil, noop, err
	}

	if err := temporal.CheckServerVersion(ctx, tc, temporal.MinServerVersion); err != nil {
		tc.Close()
		stop()

		return tcfg, nil, noop, err
	}

	return tcfg, tc, stop, nil
}

// listen starts the HTTP servers. The api role alone serves the API and
// the health endpoints on API_LISTEN_ADDR; beside other roles the API
// gets its own listener.
func listen(logger *slog.Logger, cfg *config.Config, roles config.Role, up *rolesUp, cancel context.CancelFunc) []*http.Server {
	addr := cfg.ListenAddr
	if roles == config.RoleAPI {
		addr = cfg.APIListenAddr(roles)
		up.mux.Handle(api.BaseURL+"/", up.api)
	}

	servers := []*http.Server{
		{Addr: addr, Handler: up.mux, ReadHeaderTimeout: 10 * time.Second},
		newMetricsServer(cfg.MetricsAddr),
	}

	startServer(logger, servers[0], "main", addr, cancel)
	startServer(logger, servers[1], "metrics", cfg.MetricsAddr, cancel)

	if up.api != nil && roles != config.RoleAPI {
		apiAddr := cfg.APIListenAddr(roles)
		// healthz rides along so the UI's readiness probe of the API
		// upstream works whichever topology serves it.
		apiMux := http.NewServeMux()
		apiMux.Handle("/", up.api)
		apiMux.HandleFunc("GET /healthz", handleHealthz)
		apiServer := &http.Server{Addr: apiAddr, Handler: apiMux, ReadHeaderTimeout: 10 * time.Second}
		startServer(logger, apiServer, "api", apiAddr, cancel)
		servers = append(servers, apiServer)
	}

	return servers
}

// rolesUp is what bringUpRoles started.
type rolesUp struct {
	mux    *http.ServeMux
	api    http.Handler
	worker worker.Worker
}

// bringUpRoles starts the worker, builds the API and mounts ingest and
// the health endpoints for the given roles.
func bringUpRoles(
	ctx context.Context,
	roles config.Role,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (*rolesUp, error) {
	var checks []readinessCheck
	if tc != nil {
		checks = append(checks, readinessCheck{name: "temporal", fn: func(ctx context.Context) error { return temporal.Ping(ctx, tc) }})
	}

	up := &rolesUp{mux: http.NewServeMux()}
	mux := up.mux

	var w worker.Worker

	if roles.Has(config.RoleWorker) {
		var (
			workerChecks []readinessCheck
			err          error
		)

		w, workerChecks, err = startV2Worker(ctx, cfg, tc, tcfg, strictTemplates, logger)
		if err != nil {
			return nil, err
		}

		checks = append(checks, workerChecks...)
	}

	if roles.Has(config.RoleIngest) {
		h, err := newIngestHandler(cfg, tc, tcfg.TaskQueue, logger)
		if err != nil {
			return nil, err
		}

		mux.Handle(webhookRoute, observability.Handler(h, webhookRoute))
	}

	if roles.Has(config.RoleAPI) {
		h, apiChecks, err := startAPI(ctx, cfg, roles, tc, tcfg, logger)
		if err != nil {
			return nil, err
		}

		up.api = h
		checks = append(checks, apiChecks...)
	}

	ready := newReadiness(logger, checks...)
	go ready.run(ctx, readinessInterval)

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", ready.handler(ctx))

	up.worker = w

	return up, nil
}

// startV2Worker loads the policy and engine, opens the store and starts
// a Temporal worker running every workflow and activity, then promotes
// its build in the background. It returns the worker role's readiness
// checks.
func startV2Worker(
	ctx context.Context,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (worker.Worker, []readinessCheck, error) {
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

	if wc.DevBuild() {
		logger.Warn("temporal: build ID resolved to \"dev\"; every image looks like the same version to Temporal",
			"build_id", wc.BuildID, "fix", "set TEMPORAL_BUILD_ID (the chart sets it from the image tag)")
	}

	st := pgstore.NewV2Store(pool, logger, pgstore.WithHost(cfg.GitHubHost))

	w := temporal.NewWorker(tc, &wc)
	workflows.Register(w)
	activities.New(engine, st, gh, policyVersion, logger).Register(w)
	activities.NewBudget(tc, wc.TaskQueue, cfg.RateLimitThreshold).Register(w)
	activities.NewRouter(st, tc, wc.TaskQueue, cfg.CheckInterval, cfg.RateLimitThreshold, logger).Register(w)
	activities.NewServices(&activities.ServicesConfig{
		Store: st, GitHub: gh, Temporal: tc, TaskQueue: wc.TaskQueue,
		CheckInterval: cfg.CheckInterval, PolicyVersion: policyVersion,
		SkipArchived: policyCfg.Guardian.SkipArchived, SkipForks: policyCfg.Guardian.SkipForks,
		Logger: logger,
	}).Register(w)

	if err := w.Start(); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("start temporal worker: %w", err)
	}

	svc := &serviceStarter{cfg: cfg, client: tc, store: st, taskQueue: wc.TaskQueue, logger: logger}
	if err := svc.start(ctx, policyVersion, policy.Summarize(policyCfg)); err != nil {
		w.Stop()
		pool.Close()

		return nil, nil, err
	}

	logger.Info("temporal worker started",
		"task_queue", wc.TaskQueue, "build_id", wc.BuildID,
		"activity_concurrency", wc.ActivityConcurrency, "policy_version", policyVersion)

	// Versioned workers get no tasks until their build is the
	// deployment's current version; nothing else sets it.
	go func() {
		if err := temporal.PromoteBuild(ctx, tc, wc.BuildID, logger); err != nil && ctx.Err() == nil {
			logger.Error("temporal: promoting build failed", "build_id", wc.BuildID, "error", err)
		}
	}()

	// The pool lives as long as the process; the worker is stopped first
	// on shutdown, so no activity is left holding a connection.
	go func() {
		<-ctx.Done()
		w.Stop()
		pool.Close()
	}()

	return w, workerChecks(pool, tc, wc.BuildID, time.Now()), nil
}

// deploymentReadyGrace is how long a new worker may take to become the
// deployment's current version before readiness reports the stall.
const deploymentReadyGrace = 2 * time.Minute

// workerChecks returns the worker role's readiness checks: the store
// schema, and, after deploymentReadyGrace, that this build is the
// deployment's current version. Without the second, a pod reports
// Ready while Temporal dispatches it nothing.
func workerChecks(pool *pgxpool.Pool, tc client.Client, buildID string, started time.Time) []readinessCheck {
	return []readinessCheck{
		{name: "schema", fn: func(ctx context.Context) error {
			return pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion)
		}},
		{name: "deployment", fn: func(ctx context.Context) error {
			if time.Since(started) < deploymentReadyGrace {
				return nil
			}

			return temporal.RequireCurrentVersion(ctx, tc, buildID)
		}},
	}
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
