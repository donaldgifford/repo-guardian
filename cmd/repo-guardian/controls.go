package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/config"
	pgstore "github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// controlsHalf is what differs between the evaluator and remediator
// workers (IMPL-0028 Phase 4): the role name, which also names its
// worker deployment, its task queue, and what it registers.
type controlsHalf struct {
	name      string
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
			name: cmdEvaluator, app: workflows.AppEval, taskQueue: tcfg.EvalTaskQueue, sizing: cfg.Evaluator,
			register: workflows.RegisterEvaluator,
		})
	}

	if cfg.RunsControls(roles, config.RoleRemediator) {
		halves = append(halves, controlsHalf{
			name: cmdRemediator, app: workflows.AppRemediate, taskQueue: tcfg.RemediateTaskQueue, sizing: cfg.Remediator,
			register: workflows.RegisterRemediator,
		})
	}

	return halves
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
	half controlsHalf,
	logger *slog.Logger,
) (func(), []readinessCheck, error) {
	logger = logger.With("worker", half.name)

	pcfg, err := pgxpool.ParseConfig(cfg.StoreDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: parse STORE_DSN: %w", half.name, err)
	}

	if half.sizing.DBPoolSize > 0 {
		pcfg.MaxConns = half.sizing.DBPoolSize
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: open store: %w", half.name, err)
	}

	if err := pgstore.RequireSchema(ctx, pool, pgstore.SchemaVersion); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("%s: %w", half.name, err)
	}

	wc, err := temporal.WorkerConfigFromEnv(tcfg)
	if err != nil {
		pool.Close()

		return nil, nil, err
	}

	wc.TaskQueue = half.taskQueue
	if half.sizing.Concurrency > 0 {
		wc.ActivityConcurrency = half.sizing.Concurrency
	}
	wc.Deployment = temporal.DeploymentName(half.name)

	w := temporal.NewWorker(tc, &wc)
	half.register(w)
	activities.NewBudget(tc, half.app, wc.TaskQueue, cfg.RateLimitThreshold).Register(w)

	if half.app == workflows.AppEval {
		activities.NewRemediationStarter(tc, tcfg.RemediateTaskQueue).Register(w)
	}

	if err := w.Start(); err != nil {
		pool.Close()

		return nil, nil, fmt.Errorf("%s: start temporal worker: %w", half.name, err)
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
