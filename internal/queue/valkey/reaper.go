package valkey

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
)

// ReaperOptions configures the Reaper goroutine.
type ReaperOptions struct {
	// PodID identifies the running replica. Used as the SETNX value
	// for the leader lock so a pod can identify its own held lock and
	// release it on shutdown.
	PodID string

	// Interval is the cadence between reap attempts. Default 60s.
	Interval time.Duration

	// JobAckTimeout is how long a job may stay in-flight before the
	// reaper considers it abandoned and requeues it. Default 5m.
	JobAckTimeout time.Duration

	// LockTTL is the SET NX EX TTL on the leader lock. Should be
	// larger than the worst-case reap iteration (default 30s).
	LockTTL time.Duration

	// Logger receives operational logs. nil → slog.Default().
	Logger *slog.Logger
}

// Reaper requeues stuck in-flight jobs back onto the pending list.
// One Reaper per pod; the leader-election lock keyed at
// `lock:reaper` ensures at most one runs the actual scan per
// interval, even with N replicas.
type Reaper struct {
	queue  *Queue
	opts   ReaperOptions
	logger *slog.Logger
}

// NewReaper constructs a Reaper against the given Queue. Defaults
// applied to the unset Options fields.
func NewReaper(q *Queue, opts ReaperOptions) *Reaper {
	if opts.Interval <= 0 {
		opts.Interval = 60 * time.Second
	}

	if opts.JobAckTimeout <= 0 {
		opts.JobAckTimeout = 5 * time.Minute
	}

	if opts.LockTTL <= 0 {
		opts.LockTTL = 30 * time.Second
	}

	if opts.PodID == "" {
		opts.PodID = "reaper-anon"
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Reaper{queue: q, opts: opts, logger: logger}
}

// Start runs the reap loop until ctx is cancelled. Returns ctx.Err()
// on shutdown. Errors from the reap iteration are logged at WARN and
// do not terminate the loop — Valkey hiccups should not crash the
// reaper.
func (r *Reaper) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()

	r.logger.InfoContext(ctx, "valkey reaper started",
		"pod_id", r.opts.PodID,
		"interval", r.opts.Interval,
		"ack_timeout", r.opts.JobAckTimeout,
	)

	for {
		select {
		case <-ctx.Done():
			r.logger.InfoContext(ctx, "valkey reaper stopped")

			return ctx.Err()
		case <-ticker.C:
			if err := r.reapOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				r.logger.WarnContext(ctx, "valkey reaper iteration failed", "error", err)
			}
		}
	}
}

// reapOnce attempts to acquire the leader lock and, if successful,
// requeues every in-flight entry older than JobAckTimeout and
// promotes every delayed entry whose due time has passed. The lock
// is intentionally not released early — it expires via TTL so a
// process death mid-reap leaves the lock available within LockTTL.
func (r *Reaper) reapOnce(ctx context.Context) error {
	acquired, err := r.queue.client.SetNX(
		ctx,
		r.queue.opts.ReaperLockKey,
		r.opts.PodID,
		r.opts.LockTTL,
	).Result()
	if err != nil {
		return fmt.Errorf("reaper SETNX: %w", err)
	}

	if !acquired {
		return nil
	}

	if err := r.requeueStuck(ctx); err != nil {
		return err
	}

	if err := r.promoteDue(ctx); err != nil {
		return err
	}

	// Delayed depth is leader-published (unlike queue_depth, which
	// every pod polls) so exactly one replica emits per interval.
	// Best-effort: a failed ZCARD shouldn't fail the reap.
	if depth, err := r.queue.Delayed(ctx); err == nil {
		metrics.QueueDelayedDepth.Set(float64(depth))
	} else {
		r.logger.WarnContext(ctx, "delayed depth poll failed", "error", err)
	}

	return nil
}

// requeueStuck requeues every in-flight entry older than
// JobAckTimeout back onto the jobs list. Caller holds the leader
// lock.
func (r *Reaper) requeueStuck(ctx context.Context) error {
	cutoff := time.Now().Add(-r.opts.JobAckTimeout).UnixNano()

	stuck, err := r.queue.client.ZRangeByScore(ctx, r.queue.opts.InFlightKey, &redis.ZRangeBy{
		Min: "0",
		Max: "(" + strconv.FormatInt(cutoff, 10),
	}).Result()
	if err != nil {
		return fmt.Errorf("reaper ZRANGEBYSCORE: %w", err)
	}

	if len(stuck) == 0 {
		return nil
	}

	r.logger.InfoContext(ctx, "reaper requeueing stuck jobs",
		"count", len(stuck),
		"ack_timeout", r.opts.JobAckTimeout,
	)

	for _, payload := range stuck {
		if _, err := requeueScript.Run(
			ctx,
			r.queue.client,
			[]string{r.queue.opts.InFlightKey, r.queue.opts.JobsKey},
			payload,
		).Result(); err != nil {
			r.logger.WarnContext(ctx, "reaper requeue failed", "error", err)

			return fmt.Errorf("reaper requeue: %w", err)
		}

		metrics.QueueReapedTotal.Inc()
	}

	return nil
}

// promoteDue moves every delayed-set entry at or past its due time
// back onto the jobs list. Caller holds the leader lock, so at most
// one replica promotes per interval; the Lua script keeps the move
// atomic regardless (IMPL-0022 Phase 2).
func (r *Reaper) promoteDue(ctx context.Context) error {
	promoted, err := promoteScript.Run(
		ctx,
		r.queue.client,
		[]string{r.queue.opts.DelayedKey, r.queue.opts.JobsKey},
		time.Now().UnixNano(),
	).Int64()
	if err != nil {
		return fmt.Errorf("reaper promote: %w", err)
	}

	if promoted > 0 {
		r.logger.InfoContext(ctx, "reaper promoted delayed jobs", "count", promoted)
	}

	return nil
}
