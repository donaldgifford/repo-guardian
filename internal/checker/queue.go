package checker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// Trigger describes what initiated a repo check job.
type Trigger string

const (
	// TriggerWebhook indicates the job was triggered by a GitHub webhook.
	TriggerWebhook Trigger = "webhook"

	// TriggerScheduler indicates the job was triggered by the reconciliation scheduler.
	TriggerScheduler Trigger = "scheduler"

	// TriggerManual indicates the job was triggered manually.
	TriggerManual Trigger = "manual"
)

// RepoJob represents a unit of work for the checker engine.
type RepoJob struct {
	Owner          string
	Repo           string
	InstallationID int64
	Trigger        Trigger
}

// Queue is a buffered work queue that dispatches RepoJobs to worker goroutines.
type Queue struct {
	ch     chan RepoJob
	logger *slog.Logger
	wg     sync.WaitGroup

	mu       sync.Mutex
	stopped  bool
	cancelFn context.CancelFunc
}

// NewQueue creates a Queue with the given buffer size.
func NewQueue(size int, logger *slog.Logger) *Queue {
	return &Queue{
		ch:     make(chan RepoJob, size),
		logger: logger,
	}
}

// Enqueue adds a job to the queue. Returns an error if the queue is full.
func (q *Queue) Enqueue(job RepoJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.stopped {
		return fmt.Errorf("queue is stopped")
	}

	select {
	case q.ch <- job:
		q.logger.Debug("job enqueued",
			"owner", job.Owner,
			"repo", job.Repo,
			"trigger", job.Trigger,
		)

		return nil
	default:
		return fmt.Errorf("queue is full (capacity %d)", cap(q.ch))
	}
}

// Start launches worker goroutines that pull jobs from the queue and
// call the checker engine. It blocks until the context is canceled.
func (q *Queue) Start(ctx context.Context, workers int, engine *Engine, ghClient ghclient.Client) {
	workerCtx, cancel := context.WithCancel(ctx)

	q.mu.Lock()
	q.cancelFn = cancel
	q.mu.Unlock()

	for i := range workers {
		q.wg.Add(1)

		go q.worker(workerCtx, i, engine, ghClient)
	}

	q.logger.Info("work queue started", "workers", workers, "capacity", cap(q.ch))
}

// Stop signals all workers to finish and waits for in-flight work to complete.
func (q *Queue) Stop() {
	q.mu.Lock()
	q.stopped = true

	if q.cancelFn != nil {
		q.cancelFn()
	}

	close(q.ch)
	q.mu.Unlock()
	q.wg.Wait()

	q.logger.Info("work queue stopped")
}

// Len returns the number of pending items in the queue.
func (q *Queue) Len() int {
	return len(q.ch)
}

// Accepting returns true if the queue is accepting new jobs.
func (q *Queue) Accepting() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return !q.stopped
}

func (q *Queue) worker(ctx context.Context, id int, engine *Engine, ghClient ghclient.Client) {
	defer q.wg.Done()

	log := q.logger.With("worker_id", id)
	log.Debug("worker started")

	for job := range q.ch {
		select {
		case <-ctx.Done():
			log.Debug("worker shutting down")
			return
		default:
		}

		processJob(ctx, log, engine, ghClient, job)
	}

	log.Debug("worker finished")
}

func processJob(
	ctx context.Context,
	log *slog.Logger,
	engine *Engine,
	ghClient ghclient.Client,
	job RepoJob,
) {
	start := time.Now()
	jobLog := log.With(
		"owner", job.Owner,
		"repo", job.Repo,
		"trigger", job.Trigger,
		"installation_id", job.InstallationID,
	)

	jobLog.Info("processing job")

	// Create an installation-scoped client.
	installClient, err := ghClient.CreateInstallationClient(ctx, job.InstallationID)
	if err != nil {
		jobLog.Error("failed to create installation client", "error", err)
		metrics.ErrorsTotal.WithLabelValues("create_install_client").Inc()

		return
	}

	if err := engine.CheckRepo(ctx, installClient, job.Owner, job.Repo); err != nil {
		jobLog.Error("job failed", "error", err, "duration", time.Since(start))
		metrics.ErrorsTotal.WithLabelValues("check_repo").Inc()

		return
	}

	duration := time.Since(start)
	metrics.ReposCheckedTotal.WithLabelValues(string(job.Trigger)).Inc()
	metrics.CheckDurationSeconds.Observe(duration.Seconds())
	jobLog.Info("job completed", "duration", duration)
}
