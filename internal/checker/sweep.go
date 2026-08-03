// Stale-sweep loop for the Postgres-backed multi-replica deployment.
// See DESIGN-0012 §Sweep Path and IMPL-0011 Phase 5.
//
// In the legacy single-replica path, scheduler/sweep.go enumerates
// installations + repos via the GitHub API and enqueues every repo on
// every tick. That works for small deployments but does N API calls
// per sweep where N = installations × repos, regardless of whether
// any of those repos are actually due for re-checking.
//
// StaleSweeper replaces that path. It queries
// `Store.StaleRepos(freshness, currentPolicyVersion, batchSize)` and
// enqueues only the repos whose stored state is overdue. Rate-limit
// pressure is handled downstream by the IMPL-0022 delayed-requeue
// path (throttled jobs defer with a due-time instead of being
// skipped at enqueue); the sweep only samples each installation's
// remaining budget for observability.
//
// Bootstrap (populating the store with rows for newly-installed
// repos) is the legacy Sweeper's job. The two coexist during the
// IMPL-0011 migration window.

package checker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// RateLimitProvider returns the current rate-limit budget for an
// installation. The github.Client implements this (setting the
// rate_limit_remaining{installation_id} gauge as a side effect);
// tests provide an in-memory stub.
type RateLimitProvider interface {
	RateLimitRemaining(ctx context.Context, installationID int64) (remaining, limit int, resetAt time.Time, err error)
}

// StaleSweeper reads the stale-repo list from a Store and enqueues
// each due repo.
type StaleSweeper struct {
	store         store.Store
	queue         queue.Queue
	rateLimit     RateLimitProvider
	logger        *slog.Logger
	freshness     time.Duration
	policyVersion string
	batchSize     int
}

// StaleSweeperOptions bundles the StaleSweeper constructor inputs.
type StaleSweeperOptions struct {
	Store         store.Store
	Queue         queue.Queue
	RateLimit     RateLimitProvider
	Logger        *slog.Logger
	Freshness     time.Duration
	PolicyVersion string
	BatchSize     int
}

// NewStaleSweeper constructs a StaleSweeper. Defaults are applied to
// zero-valued options. Pass-by-value is intentional — the caller
// builds a struct literal at the call site, and a pointer would buy
// nothing.
//
//nolint:gocritic // hugeParam is intentional; option-struct convention
func NewStaleSweeper(opts StaleSweeperOptions) *StaleSweeper {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if opts.Freshness <= 0 {
		opts.Freshness = 24 * time.Hour
	}

	if opts.BatchSize <= 0 {
		opts.BatchSize = 200
	}

	return &StaleSweeper{
		store:         opts.Store,
		queue:         opts.Queue,
		rateLimit:     opts.RateLimit,
		logger:        opts.Logger,
		freshness:     opts.Freshness,
		policyVersion: opts.PolicyVersion,
		batchSize:     opts.BatchSize,
	}
}

// SweepStale runs one stale-sweep iteration. Suitable for driving
// via scheduler.Scheduler.Schedule. Returns ctx.Err() on cancellation
// or the underlying store error if the StaleRepos query fails;
// per-repo errors are logged and skipped without aborting the
// iteration.
func (s *StaleSweeper) SweepStale(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// IMPL-0013 Phase 1: wipe the OpenPRsByRule gauge each sweep so
	// {org, rule} combinations that no longer have open PRs drop to
	// zero. Workers re-populate the gauge as they process enqueued
	// jobs; the gauge converges within one sweep cycle.
	metrics.ResetOpenPRsByRule()

	start := time.Now()

	stale, err := s.store.StaleRepos(ctx, s.freshness, s.policyVersion, s.batchSize)
	if err != nil {
		return fmt.Errorf("StaleRepos: %w", err)
	}

	enqueued := 0
	sampled := make(map[int64]bool)

	for i := range stale {
		repo := stale[i]

		s.sampleRateLimit(ctx, repo.InstallationID, sampled)

		job := queue.Job{
			ID: fmt.Sprintf(
				"stale-sweep/%d/%s/%s/%d",
				repo.InstallationID, repo.Owner, repo.Repo, time.Now().UnixNano(),
			),
			InstallationID: repo.InstallationID,
			Owner:          repo.Owner,
			Repo:           repo.Repo,
			Trigger:        queue.TriggerScheduler,
			EnqueuedAt:     time.Now(),
		}

		if err := s.queue.Enqueue(ctx, job); err != nil {
			s.logger.WarnContext(ctx, "stale-sweep enqueue failed",
				"installation_id", repo.InstallationID,
				"owner", repo.Owner,
				"repo", repo.Repo,
				"error", err,
			)

			continue
		}

		metrics.QueueEnqueuedTotal.WithLabelValues(queue.TriggerScheduler).Inc()
		enqueued++
	}

	metrics.SchedulerSweepBatchSize.Observe(float64(enqueued))

	s.logger.InfoContext(ctx, "stale-sweep complete",
		"stale", len(stale),
		"enqueued", enqueued,
		"freshness", s.freshness,
		"policy_version", s.policyVersion,
		"duration", time.Since(start),
	)

	return nil
}

// sampleRateLimit refreshes the rate_limit_remaining
// {installation_id} gauge once per installation per sweep — the
// gauge is set inside Client.RateLimitRemaining as a side effect.
// Observability only, never a gating decision: the IMPL-0022
// delayed-requeue path replaced the reserve gate, but this call is
// the gauge's only producer, and removing it would leave
// RepoGuardianRateLimitNearExhaustion consuming an unfed gauge
// (DESIGN-0021 OQ4 rider).
func (s *StaleSweeper) sampleRateLimit(ctx context.Context, installationID int64, sampled map[int64]bool) {
	if s.rateLimit == nil || sampled[installationID] {
		return
	}

	sampled[installationID] = true

	if _, _, _, err := s.rateLimit.RateLimitRemaining(ctx, installationID); err != nil {
		s.logger.WarnContext(ctx, "rate-limit sample failed",
			"installation_id", installationID,
			"error", err,
		)
	}
}
