package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/config"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/observability"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// controlsHalf is what differs between the evaluator and remediator
// workers (IMPL-0028 Phase 4): the role name, which also names its
// worker deployment, its task queue, and what it registers.
type controlsHalf struct {
	name      string
	role      config.Role
	app       string
	taskQueue string
	sizing    config.WorkerSizing
	register  func(worker.WorkflowRegistry)
}

// controlsHalves returns the halves roles runs under cfg, evaluator
// first.
func controlsHalves(cfg *config.Config, roles config.Role, tcfg *temporal.Config) []controlsHalf {
	var halves []controlsHalf

	if cfg.RunsControls(roles, config.RoleEvaluator) {
		halves = append(halves, controlsHalf{
			name: cmdEvaluator, role: config.RoleEvaluator, app: workflows.AppEval, taskQueue: tcfg.EvalTaskQueue, sizing: cfg.Evaluator,
			register: workflows.RegisterEvaluator,
		})
	}

	if cfg.RunsControls(roles, config.RoleRemediator) {
		halves = append(halves, controlsHalf{
			name: cmdRemediator, role: config.RoleRemediator, app: workflows.AppRemediate, taskQueue: tcfg.RemediateTaskQueue, sizing: cfg.Remediator,
			register: workflows.RegisterRemediator,
		})
	}

	return halves
}

// remediatorPoolHeadroom is the connections a remediator pool keeps
// beyond its concurrency by default, for the journal and record writes
// that run beside the claims.
const remediatorPoolHeadroom = 4

// halfSizing is the half's activity concurrency and Postgres pool size
// (0 for pgxpool's default). Every running remediation holds one
// connection for its row lock (store.Remediations.ClaimOperation), so
// the remediator defaults its pool to concurrency +
// remediatorPoolHeadroom and refuses one smaller than its concurrency,
// which would starve claims of their journal writes.
func halfSizing(half *controlsHalf) (concurrency int, pool int32, err error) {
	concurrency, pool = temporal.DefaultActivityConcurrency, half.sizing.DBPoolSize
	if half.sizing.Concurrency > 0 {
		concurrency = half.sizing.Concurrency
	}

	switch {
	case half.app != workflows.AppRemediate:
		return concurrency, pool, nil
	case pool == 0:
		return concurrency, int32(concurrency + remediatorPoolHeadroom), nil
	case int(pool) < concurrency:
		return 0, 0, fmt.Errorf(
			"%s: REMEDIATOR_DB_POOL_SIZE (%d) is smaller than REMEDIATOR_CONCURRENCY (%d): every running remediation holds a connection",
			half.name,
			pool,
			concurrency,
		)
	default:
		return concurrency, pool, nil
	}
}

// openHalfPool opens the half's Postgres pool with its own role's DSN
// (IMPL-0030 1.12) and checks the controls schema. It returns the pool
// and the half's activity concurrency.
func openHalfPool(ctx context.Context, cfg *config.Config, half *controlsHalf, logger *slog.Logger) (*pgxpool.Pool, int, error) {
	dsn, dsnEnv := cfg.WorkerStoreDSN(half.role)
	if dsn == "" {
		return nil, 0, fmt.Errorf("%s: no store DSN: set STORE_DSN_%s or STORE_DSN", half.name, strings.ToUpper(half.name))
	}

	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: parse %s: %w", half.name, dsnEnv, err)
	}

	logger.Info("controls store", "dsn_env", dsnEnv, "db_user", pcfg.ConnConfig.User)

	concurrency, poolSize, err := halfSizing(half)
	if err != nil {
		return nil, 0, err
	}

	if poolSize > 0 {
		pcfg.MaxConns = poolSize
	}

	observability.InstrumentPostgresConfig(pcfg, half.name)

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: open store: %w", half.name, err)
	}

	if err := pgstore.RequireControlsSchema(ctx, pool, pgstore.ControlsSchemaVersion); err != nil {
		pool.Close()

		return nil, 0, fmt.Errorf("%s: %w", half.name, err)
	}

	return pool, concurrency, nil
}

// startControlsWorker starts one controls half's Temporal worker on its
// own task queue, registering only that half's workflows and
// activities, then promotes its build in the background. It returns the
// function that stops the worker and closes its pool, and its readiness
// checks.
func startControlsWorker(
	ctx context.Context,
	cfg *config.Config,
	tc client.Client,
	tcfg *temporal.Config,
	half *controlsHalf,
	logger *slog.Logger,
) (func(), []readinessCheck, error) {
	logger = logger.With("worker", half.name)

	pool, concurrency, err := openHalfPool(ctx, cfg, half, logger)
	if err != nil {
		return nil, nil, err
	}

	wc := temporal.WorkerConfigFromEnv(tcfg)
	wc.TaskQueue = half.taskQueue
	wc.ActivityConcurrency = concurrency
	wc.Deployment = temporal.DeploymentName(half.name)

	w := temporal.NewWorker(tc, &wc)
	half.register(w)
	activities.NewBudget(tc, half.app, wc.TaskQueue, cfg.RateLimitThreshold).Register(w)

	var eval *evaluation

	switch half.app {
	case workflows.AppEval:
		eval, err = registerEvaluation(cfg, tc, tcfg, w, pool, logger)
	case workflows.AppRemediate:
		err = registerRemediation(ctx, cfg, w, pool, logger)
	}

	if err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("%s: %w", half.name, err)
	}

	if err := w.Start(); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("%s: start temporal worker: %w", half.name, err)
	}

	if eval != nil {
		if err := eval.start(ctx, cfg, tc, wc.TaskQueue, logger); err != nil {
			w.Stop()
			pool.Close()

			return nil, nil, fmt.Errorf("%s: %w", half.name, err)
		}
	}

	logger.Info("temporal worker started",
		"task_queue", wc.TaskQueue, "deployment", wc.Deployment, "build_id", wc.BuildID, "activity_concurrency", wc.ActivityConcurrency)

	go func() {
		if err := temporal.PromoteBuild(ctx, tc, wc.Deployment, wc.BuildID, logger); err != nil && ctx.Err() == nil {
			logger.Error("temporal: promoting build failed", "build_id", wc.BuildID, "error", err)
		}
	}()

	stop := func() {
		w.Stop()
		pool.Close()
	}

	checks := workerChecks(pool, tc, wc.Deployment, wc.BuildID, time.Now())
	for i := range checks {
		checks[i].name = half.name + "-" + checks[i].name
	}

	return stop, checks, nil
}

// evaluation is what the evaluator half loaded: the controls policy and
// the store its service workflows start from.
type evaluation struct {
	policy *policy.Snapshot
	store  *pgstore.ControlsStore
}

// registerEvaluation loads the controls policy and the Evaluation App's
// client and registers the evaluator's activities on w (IMPL-0029 task
// 7.1): evaluation, the discovery, rollout and snapshot services, the
// webhook routing of both Apps' deliveries, and the remediation starter.
func registerEvaluation(
	cfg *config.Config, tc client.Client, tcfg *temporal.Config, w worker.Registry, pool *pgxpool.Pool, logger *slog.Logger,
) (*evaluation, error) {
	snap, err := policy.Load(cfg.GuardianConfigPath, builtin.Registry(), logger)
	if err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}

	creds := cfg.Credentials(config.AppEval)

	app, err := ghclient.NewClient(creds.AppID, creds.PrivateKeyPath, logger, cfg.RateLimitThreshold)
	if err != nil {
		return nil, fmt.Errorf("evaluation app client: %w", err)
	}

	st := pgstore.NewControlsStore(pool, pgstore.WithControlsHost(cfg.GitHubHost), pgstore.WithControlsLogger(logger))
	queue := tcfg.EvalTaskQueue

	activities.NewControlsServices(&activities.ControlsServicesConfig{
		Store: st, GitHub: app, Policy: snap, Temporal: tc, TaskQueue: queue, Interval: cfg.EvalInterval, Logger: logger,
	}).Register(w)
	activities.NewEvaluator(st, snap, activities.NewGitHubEvalClients(app), logger).Register(w)

	routing := activities.NewControlsRouter(st, snap, tc, queue, cfg.EvalInterval, logger)
	activities.NewRouter(logger).WithControls(routing).WithAccessRecorder(routing).Register(w)
	activities.NewRemediationStarter(tc, tcfg.RemediateTaskQueue, st).Register(w)

	logger.Info("controls policy loaded", "path", cfg.GuardianConfigPath, "policy_version", snap.Version())

	return &evaluation{policy: snap, store: st}, nil
}

// registerRemediation registers the remediator's activities on w
// (IMPL-0030 task 3.2) as the Remediation App. Only this half registers
// them; the evaluator half never reaches the Writer. It resolves the
// App's bot user once (task 3.7) and refuses to start without it: every
// authorship check matches by that id.
func registerRemediation(ctx context.Context, cfg *config.Config, w worker.Registry, pool *pgxpool.Pool, logger *slog.Logger) error {
	snap, err := policy.Load(cfg.GuardianConfigPath, builtin.Registry(), logger)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}

	creds := cfg.Credentials(config.AppRemediate)

	app, err := ghclient.NewClient(creds.AppID, creds.PrivateKeyPath, logger, cfg.RateLimitThreshold)
	if err != nil {
		return fmt.Errorf("remediation app client: %w", err)
	}

	bot, err := app.ResolveBotIdentity(ctx)
	if err != nil {
		return fmt.Errorf("resolve the remediation app's bot user: %w", err)
	}

	logger.Info("remediation app bot user", "login", bot.Login, "id", bot.ID)
	logger.Info("Remediation App permissions required", "app", config.AppRemediate,
		"permissions", permissionStrings(remediationPermissions(snap)))

	activities.NewRemediator(&activities.RemediatorConfig{
		Store: pgstore.NewRemediationStore(pool), Policy: snap, Types: builtin.Registry(),
		Clients: activities.NewGitHubRemediationClients(app), Bot: bot, Logger: logger,
	}).Register(w)

	return nil
}

// remediationPermissions derives the Remediation App's permission set
// from the loaded policy (IMPL-0030 5.6): write on every API resource a
// direct control writes, and Workflows write only when a control writes
// under .github/workflows/ (a workflow-apply control, or a file control
// whose file lives there). GitHub refuses those commits on Contents
// write alone.
func remediationPermissions(snap *policy.Snapshot) []control.Permission {
	var (
		resources      []string
		writesWorkflow bool
	)

	for _, id := range snap.Controls() {
		def, _ := snap.Definition(id)
		writesWorkflow = writesWorkflow || def.Apply == control.ApplyWorkflow

		for _, res := range snap.Control(id).Owns() {
			switch {
			case res.Kind == control.KindFile:
				writesWorkflow = writesWorkflow || strings.HasPrefix(res.Key, ".github/workflows/")
			case def.Apply == control.ApplyDirect:
				resources = append(resources, control.PermissionResource(res.Kind))
			}
		}
	}

	return control.RemediationPermissions(resources, writesWorkflow)
}

// start reconciles the evaluator's Schedules and starts the rollout of
// the activated policy. Every evaluator pod runs it: each step is
// idempotent, and Temporal rejects the duplicate starts.
func (e *evaluation) start(ctx context.Context, cfg *config.Config, tc client.Client, taskQueue string, logger *slog.Logger) error {
	svc := &serviceStarter{cfg: cfg, client: tc, taskQueue: taskQueue, logger: logger}
	if err := svc.ensureSchedules(ctx, config.RoleEvaluator); err != nil {
		return err
	}

	return svc.startControlsRollout(ctx, e.store, e.policy.Version())
}
