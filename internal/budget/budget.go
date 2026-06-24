// Package budget implements the per-installation GitHub API rate-limit
// budget tracker introduced in IMPL-0015 / DESIGN-0017. It is the
// "Layer 1" guard that prevents the StaleSweeper and Discoverer from
// burning the hourly rate-limit window before workers get a chance to
// process the existing queue depth.
//
// A Tracker is leader-scoped: only the pod holding the
// `scheduler:leader` lock instantiates one. The tracker caches the
// last-known rate-limit snapshot per installation and refreshes via
// `RateLimitClient.RateLimitRemaining` when (a) the cached snapshot
// is empty, (b) the snapshot's `resetAt` is in the past (the
// GitHub-reported hourly window has rolled), or (c) the caller
// explicitly invokes RefreshFromAPI.
//
// The tracker is safe for concurrent use; callers in goroutines may
// SpendableForEnqueue / Decrement freely against the same instance.
package budget

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// ErrNoSnapshot signals that the caller queried SpendableForEnqueue
// before any snapshot was fetched. The caller MUST treat this as
// "fall open" — return a positive integer so the gate does not
// gratuitously starve enqueues when the rate-limit lookup hasn't
// run yet.
var ErrNoSnapshot = errors.New("budget: no rate-limit snapshot yet")

// RateLimitClient is the minimal surface Tracker needs from
// github.Client. Tests provide an in-memory stub; the production
// wiring binds this to *github.GitHubClient via the same method.
type RateLimitClient interface {
	RateLimitRemaining(ctx context.Context, installationID int64) (remaining, limit int, resetAt time.Time, err error)
}

// Snapshot is a cached rate-limit observation for a single
// installation. Zero-value Snapshot.LastFetchedAt means "never
// observed" — callers must RefreshFromAPI before consulting
// SpendableForEnqueue.
type Snapshot struct {
	Remaining     int
	Limit         int
	ResetAt       time.Time
	LastFetchedAt time.Time
}

// Tracker holds per-installation rate-limit snapshots and exposes
// the spendable / decrement / refresh primitives. The reserve
// fraction is operator-tunable (DISCOVERY_RESERVE_FRACTION env var)
// and bounds the gate at `Limit * (1 - reserveFraction)` — anything
// below that floor blocks enqueue.
//
// CostPerRepo is the operator's estimate of how many API calls a
// single repo's reconcile burns. Decrement subtracts this value;
// SpendableForEnqueue divides the remaining budget by CostPerRepo to
// return an enqueue-able count.
type Tracker struct {
	reserveFraction float64
	costPerRepo     int

	mu        sync.Mutex
	snapshots map[int64]Snapshot
}

// Options configures a Tracker. ReserveFraction must be in [0, 1];
// CostPerRepo must be > 0.
type Options struct {
	// ReserveFraction is the fraction of the rate-limit limit that
	// the tracker holds in reserve for worker-driven API calls.
	// SpendableForEnqueue returns 0 when remaining falls below
	// `Limit * ReserveFraction`.
	ReserveFraction float64

	// CostPerRepo is the operator's estimate of the rate-limit cost
	// of a single reconcile job. Larger values cause the gate to
	// close sooner.
	CostPerRepo int
}

// New constructs a Tracker. Options must be valid (validated via
// the chart values.schema.json and config.Load); New panics on
// nonsense inputs to surface misconfiguration at startup rather
// than mid-tick.
func New(opts Options) *Tracker {
	if opts.ReserveFraction < 0 || opts.ReserveFraction > 1 {
		panic("budget: ReserveFraction must be in [0, 1]")
	}

	if opts.CostPerRepo <= 0 {
		panic("budget: CostPerRepo must be > 0")
	}

	return &Tracker{
		reserveFraction: opts.ReserveFraction,
		costPerRepo:     opts.CostPerRepo,
		snapshots:       make(map[int64]Snapshot),
	}
}

// RefreshFromAPI fetches the current rate-limit budget for
// installationID and caches it. Always replaces the existing
// snapshot; callers that want to avoid redundant refreshes should
// call HasFreshSnapshot first.
func (t *Tracker) RefreshFromAPI(ctx context.Context, client RateLimitClient, installationID int64) error {
	remaining, limit, resetAt, err := client.RateLimitRemaining(ctx, installationID)

	outcome := "ok"
	if err != nil {
		outcome = "error"
	}

	metrics.APIBudgetRefreshTotal.WithLabelValues(installationIDLabel(installationID), outcome).Inc()

	if err != nil {
		return err
	}

	now := time.Now()

	snap := Snapshot{
		Remaining:     remaining,
		Limit:         limit,
		ResetAt:       resetAt,
		LastFetchedAt: now,
	}

	t.mu.Lock()
	t.snapshots[installationID] = snap
	t.mu.Unlock()

	t.publishGauges(installationID, snap)

	return nil
}

// SpendableForEnqueue returns the number of additional enqueues the
// tracker will allow without breaching the reserve fraction.
// Returns (0, ErrNoSnapshot) when no snapshot has been cached for
// this installation; callers MUST treat that as "fall open" and
// allow the enqueue rather than starving the queue.
//
// When the cached snapshot's ResetAt is in the past the tracker
// returns ErrNoSnapshot too — the operator-tunable refresh path
// (callers checking the returned error) is the path that re-fetches.
func (t *Tracker) SpendableForEnqueue(installationID int64) (int, error) {
	t.mu.Lock()
	snap, ok := t.snapshots[installationID]
	t.mu.Unlock()

	if !ok || snap.LastFetchedAt.IsZero() {
		return 0, ErrNoSnapshot
	}

	if !snap.ResetAt.IsZero() && time.Now().After(snap.ResetAt) {
		// Stale — caller should RefreshFromAPI and retry.
		return 0, ErrNoSnapshot
	}

	if snap.Limit <= 0 {
		// Unknown limit — fall open.
		return 0, ErrNoSnapshot
	}

	floor := int(float64(snap.Limit) * t.reserveFraction)
	usable := snap.Remaining - floor

	if usable <= 0 {
		return 0, nil
	}

	spendable := usable / t.costPerRepo

	metrics.APIBudgetSpendable.
		WithLabelValues(installationIDLabel(installationID)).
		Set(float64(spendable))

	return spendable, nil
}

// Decrement subtracts costPerRepo from the cached Remaining for
// installationID. Best-effort: when no snapshot exists the call is
// a no-op (the tracker will refresh on the next SpendableForEnqueue
// → ErrNoSnapshot → caller-driven refresh cycle).
func (t *Tracker) Decrement(installationID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	snap, ok := t.snapshots[installationID]
	if !ok {
		return
	}

	snap.Remaining -= t.costPerRepo
	if snap.Remaining < 0 {
		snap.Remaining = 0
	}

	t.snapshots[installationID] = snap

	metrics.APIBudgetRemaining.
		WithLabelValues(installationIDLabel(installationID)).
		Set(float64(snap.Remaining))
}

// publishGauges updates the metrics that operators read for budget
// visibility. Called both on refresh and on Decrement.
func (t *Tracker) publishGauges(installationID int64, snap Snapshot) {
	label := installationIDLabel(installationID)

	metrics.APIBudgetRemaining.WithLabelValues(label).Set(float64(snap.Remaining))
	metrics.APIBudgetReserveFraction.WithLabelValues(label).Set(t.reserveFraction)

	if snap.Limit > 0 {
		utilisation := 1.0 - float64(snap.Remaining)/float64(snap.Limit)
		metrics.APIBudgetUtilisation.WithLabelValues(label).Set(utilisation)
	}
}
