// Package worker implements the in-process worker pool that consumes
// reconcile jobs from a queue.Queue and dispatches them to the
// checker engine. Renamed from internal/checker/queue.go's
// Queue.worker under IMPL-0011 Phase 1 (Open Q5 resolution).
//
// The pool is queue-backend agnostic — both the in-memory channel
// queue and the future Valkey queue implement the same
// queue.Queue.Subscribe shape. Worker count comes from
// WORKER_CONCURRENCY (mapped from config.Config.WorkerCount today
// for backward compat).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// Pool runs N worker goroutines that subscribe to a queue.Queue and
// invoke engine.CheckRepo for each delivered job. Idempotent Stop.
type Pool struct {
	queue    queue.Queue
	engine   *checker.Engine
	ghClient ghclient.Client
	logger   *slog.Logger
	workers  int

	wg     sync.WaitGroup
	cancel context.CancelFunc
	mu     sync.Mutex
	done   bool
}

// New constructs a Pool wired to the given queue, engine, and client.
// Use Start to launch the workers; Stop to drain and shut down.
func New(q queue.Queue, engine *checker.Engine, ghClient ghclient.Client, workers int, logger *slog.Logger) *Pool {
	return &Pool{
		queue:    q,
		engine:   engine,
		ghClient: ghClient,
		workers:  workers,
		logger:   logger,
	}
}

// Start launches the worker pool. Each worker runs an independent
// queue.Subscribe loop; the loops exit when ctx is cancelled or the
// queue is closed.
func (p *Pool) Start(ctx context.Context) {
	wctx, cancel := context.WithCancel(ctx)

	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	for i := range p.workers {
		p.wg.Add(1)

		go p.run(wctx, i)
	}

	p.logger.Info("worker pool started", "workers", p.workers)
}

// Stop signals every worker to drain and waits for in-flight jobs
// to complete. Idempotent.
func (p *Pool) Stop() {
	p.mu.Lock()

	if p.done {
		p.mu.Unlock()

		return
	}

	p.done = true

	if p.cancel != nil {
		p.cancel()
	}

	p.mu.Unlock()
	p.wg.Wait()
	p.logger.Info("worker pool stopped")
}

func (p *Pool) run(ctx context.Context, id int) {
	defer p.wg.Done()

	log := p.logger.With("worker_id", id)
	log.Debug("worker started")

	if err := p.queue.Subscribe(ctx, func(jobCtx context.Context, j queue.Job) error {
		return p.processJob(jobCtx, log, j)
	}); err != nil && ctx.Err() == nil {
		log.Warn("queue subscribe ended with error", "error", err)
	}

	log.Debug("worker finished")
}

// processJob runs a single queue.Job through the engine; the
// queue.Job-by-value signature mirrors the queue.Queue.Subscribe
// handler contract per DESIGN-0012.
//
//nolint:gocritic // hugeParam: by-value matches Subscribe's handler signature
func (p *Pool) processJob(ctx context.Context, log *slog.Logger, j queue.Job) error {
	start := time.Now()
	jobLog := log.With(
		"owner", j.Owner,
		"repo", j.Repo,
		"trigger", j.Trigger,
		"installation_id", j.InstallationID,
		"job_id", j.ID,
	)

	jobLog.Info("processing job")

	installClient, err := p.ghClient.CreateInstallationClient(ctx, j.InstallationID)
	if err != nil {
		jobLog.Error("failed to create installation client", "error", err)
		metrics.ErrorsTotal.WithLabelValues("create_install_client", j.Owner).Inc()

		return fmt.Errorf("create installation client for %d: %w", j.InstallationID, err)
	}

	if err := p.engine.CheckRepo(ctx, installClient, j.Owner, j.Repo); err != nil {
		jobLog.Error("job failed", "error", err, "duration", time.Since(start))
		metrics.ErrorsTotal.WithLabelValues("check_repo", j.Owner).Inc()

		return fmt.Errorf("check %s/%s: %w", j.Owner, j.Repo, err)
	}

	duration := time.Since(start)
	metrics.ReposCheckedTotal.WithLabelValues(j.Trigger, j.Owner).Inc()
	metrics.CheckDurationSeconds.Observe(duration.Seconds())
	jobLog.Info("job completed", "duration", duration)

	return nil
}
