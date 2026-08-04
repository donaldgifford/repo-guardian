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
//
// IMPL-0015 Phase 0 added the persistent state write-back contract.
// After every processed job the worker calls
// stateStore.UpdateRepoState with the engine outcome and the policy
// version under which the check ran, so the stale-sweeper can
// identify converged vs drifted repos on the next tick. Both success
// and error paths write back; Store-write failures are logged and
// counted but never propagated up — the queue.Job is the source of
// truth for "did we do the work", and the persisted state is best-
// effort observability.
package worker

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// errMaxRunes bounds the persisted LastError width. Kept short so the
// operator-facing dashboards / log scrapers see predictable rows;
// full diagnostic context lives in the slog.Error from processJob.
const errMaxRunes = 1024

// Pool runs N worker goroutines that subscribe to a queue.Queue and
// invoke engine.CheckRepo for each delivered job. Idempotent Stop.
type Pool struct {
	queue         queue.Queue
	engine        *checker.Engine
	ghClient      ghclient.Client
	stateStore    store.Store
	policyVersion string
	logger        *slog.Logger
	workers       int
	maxAttempts   int

	wg     sync.WaitGroup
	cancel context.CancelFunc
	mu     sync.Mutex
	done   bool
}

// New constructs a Pool wired to the given queue, engine, client,
// state store, and policy version. The state store + policy version
// drive the per-job write-back added in IMPL-0015 Phase 0; pass a nil
// stateStore in tests that don't exercise that path. maxJobAttempts
// is the MAX_JOB_ATTEMPTS retry cap (IMPL-0022); a job delivered with
// Attempts at or past it takes the terminal disposition instead of
// being processed. Zero disables the cap — config validation rejects
// that in production, so only tests run uncapped.
//
// Use Start to launch the workers; Stop to drain and shut down.
func New(
	q queue.Queue,
	engine *checker.Engine,
	ghClient ghclient.Client,
	stateStore store.Store,
	policyVersion string,
	maxJobAttempts int,
	workers int,
	logger *slog.Logger,
) *Pool {
	return &Pool{
		queue:         q,
		engine:        engine,
		ghClient:      ghClient,
		stateStore:    stateStore,
		policyVersion: policyVersion,
		maxAttempts:   maxJobAttempts,
		workers:       workers,
		logger:        logger,
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

	if p.maxAttempts > 0 && j.Attempts >= p.maxAttempts {
		return p.dropExhausted(ctx, jobLog, j)
	}

	installClient, err := p.ghClient.CreateInstallationClient(ctx, j.InstallationID)
	if err != nil {
		jobLog.Error("failed to create installation client", "error", err)
		metrics.ErrorsTotal.WithLabelValues("create_install_client", j.Owner).Inc()
		p.writeBack(ctx, jobLog, j, err)

		return fmt.Errorf("create installation client for %d: %w", j.InstallationID, err)
	}

	if err := p.engine.CheckRepo(ctx, installClient, j.Owner, j.Repo); err != nil {
		if retry := p.deferralFor(jobLog, &j, err); retry != nil {
			return retry
		}

		// Access denial is terminal, so it must not take the retry path
		// below. Order matters: deferralFor runs first because a
		// secondary rate limit is also a 403 (INV-0015).
		if ghclient.IsAccessDenied(err) {
			return p.dropInaccessible(ctx, jobLog, j, err)
		}

		jobLog.Error("job failed", "error", err, "duration", time.Since(start))
		metrics.ErrorsTotal.WithLabelValues("check_repo", j.Owner).Inc()
		p.writeBack(ctx, jobLog, j, err)

		return fmt.Errorf("check %s/%s: %w", j.Owner, j.Repo, err)
	}

	duration := time.Since(start)
	metrics.ReposCheckedTotal.WithLabelValues(j.Trigger, j.Owner).Inc()
	metrics.CheckDurationSeconds.Observe(duration.Seconds())
	p.writeBack(ctx, jobLog, j, nil)
	jobLog.Info("job completed", "duration", duration)

	return nil
}

// dropInaccessible parks a repository the App cannot reach.
//
// Without this the job takes the generic error path: nack, requeue,
// Attempts++, up to MAX_JOB_ATTEMPTS — and then the next stale sweep
// hands it straight back, so an inaccessible repository burns the whole
// attempt budget every cycle, forever, and its failures are
// indistinguishable from a transient 500 in both logs and metrics.
//
// Returns nil so the queue acks and drops, exactly as dropExhausted
// does; returning an error here would rebuild the retry loop this
// exists to break.
//
//nolint:gocritic // hugeParam: queue.Job-by-value matches Subscribe's contract
func (p *Pool) dropInaccessible(ctx context.Context, log *slog.Logger, j queue.Job, cause error) error {
	log.Error("repository is not accessible to this installation; parking it until discovery sees it again",
		"error", cause,
	)

	metrics.RepoAccessDeniedTotal.WithLabelValues(j.Owner, strconv.FormatInt(j.InstallationID, 10)).Inc()

	p.writeBack(ctx, log, j, fmt.Errorf("repository not accessible to installation %d: %w", j.InstallationID, cause))

	// Best-effort, and deliberately after the write-back: if this fails
	// the repo simply stays in the sweep and we retry next cycle, which
	// is the pre-existing behaviour rather than a new failure mode.
	if p.stateStore != nil {
		if err := p.stateStore.Deactivate(ctx, j.InstallationID, j.Owner, j.Repo); err != nil {
			log.Warn("deactivate failed; repo stays in the sweep and will retry", "error", err)
		}
	}

	return nil
}

// dropExhausted takes the terminal disposition for a job delivered at
// or past the MAX_JOB_ATTEMPTS cap: a descriptive StatusError row via
// the best-effort writeBack, an exhausted-counter increment, and a
// nil return so the queue acks and drops the job. The next stale
// sweep re-enqueues the repo naturally if it is still due — this is
// what makes a nack-looping job (e.g. a dead installation) self-heal
// instead of retrying forever (INV-0012 finding K, DESIGN-0021 OQ2).
//
//nolint:gocritic // hugeParam: queue.Job-by-value matches Subscribe's contract
func (p *Pool) dropExhausted(ctx context.Context, log *slog.Logger, j queue.Job) error {
	log.Error("job exceeded attempt cap; dropping with terminal status",
		"attempts", j.Attempts,
		"max_attempts", p.maxAttempts,
	)

	p.writeBack(ctx, log, j, fmt.Errorf(
		"job dropped after %d attempts (MAX_JOB_ATTEMPTS=%d); see worker logs for the underlying failures",
		j.Attempts, p.maxAttempts))

	metrics.QueueAttemptsExhaustedTotal.WithLabelValues(strconv.FormatInt(j.InstallationID, 10)).Inc()

	return nil
}

// deferralFor translates a rate-limit throttle surfaced by CheckRepo
// into the queue's deferral signal (DESIGN-0021 Phase 3→4 handoff,
// via ghclient.AsThrottled so both throttle shapes are caught). The
// due time is GitHub's own reset plus a uniform jitter of
// [0, min(delay/4, 60s)) so a fleet throttled by the same reset
// instant doesn't thundering-herd the first promotion tick after it.
//
// Returns nil when err carries no throttle signal — the caller nacks
// as before. A deferral is not a failure: no error metrics and no
// repo_state write-back, because the check never ran.
func (*Pool) deferralFor(log *slog.Logger, j *queue.Job, err error) *queue.RetryAfterError {
	thr, ok := ghclient.AsThrottled(err)
	if !ok {
		return nil
	}

	delay := time.Until(thr.ResetAt)
	if delay <= 0 {
		// Stale or absent reset — no server-supplied delay to
		// honour; fall back to attempt-keyed exponential backoff
		// (IMPL-0022 OQ3).
		delay = backoffDelay(j.Attempts)
	}

	due := time.Now().Add(delay + retryJitter(delay))

	log.Info("rate limit throttled; deferring job",
		"reset_at", thr.ResetAt,
		"due", due,
		"attempts", j.Attempts,
		"remaining", thr.Remaining,
		"limit", thr.Limit,
	)

	return &queue.RetryAfterError{After: due, Reason: "rate_limit", Err: err}
}

// Backoff shape for deferrals with no usable server-supplied reset
// (IMPL-0022 OQ3): base 30s doubling per burned attempt to a 30m cap.
const (
	backoffBase = 30 * time.Second
	backoffCap  = 30 * time.Minute
)

// backoffDelay returns min(backoffBase × 2^attempts, backoffCap).
// The doubling loop (rather than a shift) sidesteps overflow for
// adversarial attempt counts; MAX_JOB_ATTEMPTS caps it long before
// that matters in practice.
func backoffDelay(attempts int) time.Duration {
	d := backoffBase

	for range attempts {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}

	return d
}

// retryJitter returns a uniform draw from [0, min(delay/4, 60s)) —
// the IMPL-0022 OQ3 jitter shape shared by the reset-anchored and
// backoff deferral paths. Uses crypto/rand per the repo's jitter
// convention (see webhook.randomJitter); the reader-failure path
// collapses to no jitter, fail-safe for a load-spreading optimisation.
func retryJitter(delay time.Duration) time.Duration {
	span := min(delay/4, time.Minute)
	if span <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(span)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

// writeBack persists the per-job outcome to the Store. checkErr=nil
// is the success path (StatusSuccess, empty LastError); checkErr!=nil
// is the error path (StatusError, truncated error message). Best-
// effort: a Store-write failure logs at Warn, counts under
// store_writeback_total{outcome="error"}, and returns. The queue.Job
// is the source of truth for "did we do the work" — losing the
// persisted state just means the next sweep will re-enqueue.
//
//nolint:gocritic // hugeParam: queue.Job-by-value matches Subscribe's contract
func (p *Pool) writeBack(ctx context.Context, log *slog.Logger, j queue.Job, checkErr error) {
	if p.stateStore == nil {
		return
	}

	now := time.Now().UTC()

	state := &store.RepoState{
		InstallationID: j.InstallationID,
		Owner:          j.Owner,
		Repo:           j.Repo,
		LastCheckedAt:  &now,
		PolicyVersion:  p.policyVersion,
	}

	if checkErr == nil {
		state.LastCheckStatus = store.StatusSuccess
	} else {
		state.LastCheckStatus = store.StatusError
		state.LastError = store.Truncate(checkErr.Error(), errMaxRunes)
	}

	installLabel := strconv.FormatInt(j.InstallationID, 10)
	wbStart := time.Now()

	err := p.stateStore.UpdateRepoState(ctx, state)
	metrics.StoreWritebackDurationSeconds.Observe(time.Since(wbStart).Seconds())

	outcome := "ok"
	if err != nil {
		outcome = "error"

		log.Warn("store write-back failed; sweep will re-enqueue", "error", err)
	}

	metrics.StoreWritebackTotal.WithLabelValues(installLabel, outcome).Inc()
}
