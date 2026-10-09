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

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/control"
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

	fs.Usage = func() {
		usage(fs.Output())
		fs.PrintDefaults()
	}

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

	tcfg, tc, err := dialTemporal(ctx, roles, obs, logger)
	if err != nil {
		return err
	}

	up, err := bringUpRoles(ctx, roles, cfg, tc, &tcfg, *strictTemplates, logger)
	if err != nil {
		if tc != nil {
			tc.Close()
		}

		return err
	}

	servers := listen(logger, cfg, roles, up, cancel)

	awaitShutdown(ctx, logger)
	cancel()
	stopRoles(logger, up.stopWorker, tc, servers...)

	return nil
}

// dialTemporal connects to Temporal for the roles that use it. The api
// role alone does not: it reads Postgres only.
func dialTemporal(
	ctx context.Context, roles config.Role, obs *observability.Provider, logger *slog.Logger,
) (temporal.Config, client.Client, error) {
	tcfg, err := temporal.ConfigFromEnv()
	if roles == config.RoleAPI {
		// The api role never dials, so an unset or incomplete Temporal
		// env is not an error for it: the chart gives it none.
		return tcfg, nil, nil
	}

	if err != nil {
		return tcfg, nil, err
	}

	tc, err := temporal.Dial(ctx, &tcfg, temporal.DialOptions{Logger: logger, MeterProvider: obs.MeterProvider})
	if err != nil {
		return tcfg, nil, err
	}

	if err := temporal.CheckServerVersion(ctx, tc, temporal.MinServerVersion); err != nil {
		tc.Close()

		return tcfg, nil, err
	}

	return tcfg, tc, nil
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

// rolesUp is what bringUpRoles started. stopWorker is nil unless the
// worker role runs.
type rolesUp struct {
	mux        *http.ServeMux
	api        http.Handler
	stopWorker func()
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

	logAppPermissions(logger, roles)

	if roles.Has(config.RoleWorker) {
		stop, workerChecks, err := startV2Worker(ctx, cfg, tc, tcfg, strictTemplates, logger)
		if err != nil {
			return nil, err
		}

		up.stopWorker = stop
		checks = append(checks, workerChecks...)
	}

	// A failure after the worker started stops it again.
	fail := func(err error) (*rolesUp, error) {
		if up.stopWorker != nil {
			up.stopWorker()
		}

		return nil, err
	}

	if roles.Has(config.RoleIngest) {
		routes, err := newIngestRoutes(cfg, tc, tcfg.TaskQueue, logger)
		if err != nil {
			return fail(err)
		}

		for route, h := range routes {
			mux.Handle(route, observability.Handler(h, route))
		}
	}

	if roles.Has(config.RoleAPI) {
		h, apiChecks, err := startAPI(ctx, cfg, roles, tc, tcfg, logger)
		if err != nil {
			return fail(err)
		}

		up.api = h
		checks = append(checks, apiChecks...)
	}

	ready := newReadiness(logger, checks...)
	go ready.run(ctx, readinessInterval)

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", ready.handler(ctx))

	return up, nil
}

// startV2Worker loads the policy and engine, opens the store and starts
// a Temporal worker running every workflow and activity, then promotes
// its build in the background. It returns the function that stops the
// worker and then closes the pool, and the worker role's readiness
// checks.
func startV2Worker(
	ctx context.Context,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	strictTemplates bool,
	logger *slog.Logger,
) (func(), []readinessCheck, error) {
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

	// The pool lives as long as the worker; stopping the worker first
	// means no activity is left holding a connection.
	stop := func() {
		w.Stop()
		pool.Close()
	}

	return stop, workerChecks(pool, tc, wc.BuildID, time.Now()), nil
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

// logAppPermissions logs, once at startup, the App permission set each
// running role acts with (IMPL-0028 task 3.4), so an operator can check
// the installation against it. The Remediation App's set is derived
// from the registered control types; until IMPL-0029 registers any it is
// the base set. The rc's worker acts as both.
func logAppPermissions(logger *slog.Logger, roles config.Role) {
	if roles&(config.RoleEvaluator|config.RoleWorker) != 0 {
		logger.Info("Evaluation App permissions required", "app", config.AppEval,
			"permissions", permissionStrings(control.EvaluationPermissions()))
	}

	if roles&(config.RoleRemediator|config.RoleWorker) != 0 {
		logger.Info("Remediation App permissions required", "app", config.AppRemediate,
			"permissions", permissionStrings(control.RemediationPermissions(nil, false)))
	}
}

func permissionStrings(ps []control.Permission) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}

	return out
}

// newIngestRoutes builds the webhook handlers by route: the rc's
// single-App route, plus one route per controls App whose webhook
// secret is set (IMPL-0028 task 3.2, D29). Each validates with its own
// secret only. The policy is read only for its watched paths, so a
// policy error fails startup rather than silently dropping every push.
func newIngestRoutes(cfg *config.Config, tc client.Client, taskQueue string, logger *slog.Logger) (map[string]http.Handler, error) {
	policyCfg, err := policy.Load(cfg.GuardianConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load policy for watched paths: %w", err)
	}

	watched := policy.ExtractWatchedPaths(policyCfg)

	routes := map[string]http.Handler{
		webhookRoute: ingest.New(cfg.GitHubWebhookSecret, tc, taskQueue, watched, logger),
	}

	for _, app := range []config.App{config.AppEval, config.AppRemediate} {
		creds := cfg.Credentials(app)
		if creds.WebhookSecret == "" {
			continue
		}

		routes[webhookRoute+"/"+string(app)] = ingest.NewApp(string(app), creds.AppID, creds.WebhookSecret, tc, taskQueue, watched, logger)
	}

	return routes, nil
}

// stopRoles shuts a process down in dependency order: the worker drains
// in-flight tasks (bounded by shutdownTimeout) and closes its pool, then
// the Temporal client closes, then the HTTP servers shut down. Servers
// go last so readiness and metrics stay answerable while work drains.
func stopRoles(logger *slog.Logger, stopWorker func(), tc client.Client, servers ...*http.Server) {
	if stopWorker != nil {
		drainWorker(logger, stopWorker, shutdownTimeout)
	}

	if tc != nil {
		tc.Close()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown error", "addr", srv.Addr, "error", err)
		}
	}

	logger.Info("repo-guardian stopped")
}

// drainWorker runs stop and waits up to timeout for it. A worker that
// outlives the bound is abandoned: the process is exiting, and Temporal
// retries whatever the abandoned activities had in flight.
func drainWorker(logger *slog.Logger, stop func(), timeout time.Duration) {
	done := make(chan struct{})

	go func() {
		defer close(done)
		stop()
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		logger.Warn("worker did not drain before the shutdown timeout", "timeout", timeout)
	}
}

func shutdownObservability(logger *slog.Logger, obs *observability.Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), observabilityShutdownTimeout)
	defer cancel()

	if err := obs.Shutdown(ctx); err != nil {
		logger.Warn("observability shutdown error", "error", err)
	}
}
