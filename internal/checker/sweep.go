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
// enqueues only the repos whose stored state is overdue. The
// rate-limit reserve gate further skips repos whose installation has
// burned through its hourly budget.
//
// Bootstrap (populating the store with rows for newly-installed
// repos) is the legacy Sweeper's job. The two coexist during the
// IMPL-0011 migration window.

package checker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/budget"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// RateLimitProvider returns the current rate-limit budget for an
// installation. The github.Client implements this; tests provide an
// in-memory stub. Returning a negative remaining means "unknown" —
// the gate falls open.
type RateLimitProvider interface {
	RateLimitRemaining(ctx context.Context, installationID int64) (remaining, limit int, resetAt time.Time, err error)
}

// StaleSweeper reads the stale-repo list from a Store and enqueues
// the survivors of the per-installation rate-limit reserve gate. An
// optional *budget.Tracker provides per-enqueue gating from a cached
// rate-limit snapshot (IMPL-0015 Phase 1.2); when nil the sweeper
// falls back to the simpler reserve gate via RateLimitProvider.
type StaleSweeper struct {
	store         store.Store
	queue         queue.Queue
	rateLimit     RateLimitProvider
	budget        *budget.Tracker
	logger        *slog.Logger
	freshness     time.Duration
	policyVersion string
	batchSize     int
	reserve       float64
}

// StaleSweeperOptions bundles the StaleSweeper constructor inputs.
type StaleSweeperOptions struct {
	Store         store.Store
	Queue         queue.Queue
	RateLimit     RateLimitProvider
	Budget        *budget.Tracker
	Logger        *slog.Logger
	Freshness     time.Duration
	PolicyVersion string
	BatchSize     int
	Reserve       float64
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

	if opts.Reserve <= 0 {
		opts.Reserve = 0.1
	}

	return &StaleSweeper{
		store:         opts.Store,
		queue:         opts.Queue,
		rateLimit:     opts.RateLimit,
		budget:        opts.Budget,
		logger:        opts.Logger,
		freshness:     opts.Freshness,
		policyVersion: opts.PolicyVersion,
		batchSize:     opts.BatchSize,
		reserve:       opts.Reserve,
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
	gated := 0
	budgetGated := 0

	for i := range stale {
		repo := stale[i]

		if !s.allowedByRateLimit(ctx, repo.InstallationID) {
			gated++

			continue
		}

		if !s.allowedByBudget(ctx, repo.InstallationID) {
			budgetGated++

			continue
		}

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

		if s.budget != nil {
			s.budget.Decrement(repo.InstallationID)
		}

		metrics.QueueEnqueuedTotal.WithLabelValues(queue.TriggerScheduler).Inc()
		enqueued++
	}

	metrics.SchedulerSweepBatchSize.Observe(float64(enqueued))

	s.logger.InfoContext(ctx, "stale-sweep complete",
		"stale", len(stale),
		"enqueued", enqueued,
		"gated_rate_limit", gated,
		"gated_budget", budgetGated,
		"freshness", s.freshness,
		"policy_version", s.policyVersion,
		"duration", time.Since(start),
	)

	return nil
}

// allowedByBudget consults the optional BudgetTracker. Returns true
// (allow) when no tracker is wired, the snapshot is missing/stale
// (ErrNoSnapshot — fall open), or the tracker has at least one
// spendable enqueue. Returns false (gate closed) and increments
// enqueue_gated_by_budget_total when the tracker reports zero
// spendable slots.
//
// Note: this gate is in ADDITION to allowedByRateLimit. The rate-
// limit reserve catches "the actual API budget is below floor"
// via the live RateLimitProvider call; the budget tracker catches
// "the projected budget after expected per-repo cost is below
// floor" via the cached snapshot + local Decrement accounting.
func (s *StaleSweeper) allowedByBudget(ctx context.Context, installationID int64) bool {
	if s.budget == nil {
		return true
	}

	spendable, err := s.budget.SpendableForEnqueue(installationID)
	if err != nil {
		if errors.Is(err, budget.ErrNoSnapshot) {
			return true // fall open — caller drives refresh elsewhere
		}

		s.logger.WarnContext(ctx, "budget gate lookup failed; falling open",
			"installation_id", installationID,
			"error", err,
		)

		return true
	}

	if spendable > 0 {
		return true
	}

	metrics.EnqueueGatedByBudgetTotal.
		WithLabelValues(strconv.FormatInt(installationID, 10)).
		Inc()

	return false
}

// allowedByRateLimit returns true if the installation has at least
// `reserve × limit` of budget left. Errors fall open (true) so a
// transient rate-limit lookup glitch doesn't halt the sweep.
func (s *StaleSweeper) allowedByRateLimit(ctx context.Context, installationID int64) bool {
	if s.rateLimit == nil {
		return true
	}

	remaining, limit, _, err := s.rateLimit.RateLimitRemaining(ctx, installationID)
	if err != nil {
		s.logger.WarnContext(ctx, "rate-limit lookup failed; falling open",
			"installation_id", installationID,
			"error", err,
		)

		return true
	}

	if limit <= 0 {
		return true
	}

	threshold := int(float64(limit) * s.reserve)
	if remaining > threshold {
		return true
	}

	metrics.RateLimitReserveBlockedTotal.
		WithLabelValues(strconv.FormatInt(installationID, 10)).
		Inc()

	return false
}
