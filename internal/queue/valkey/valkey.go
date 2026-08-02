// Package valkey provides a durable queue.Queue implementation backed
// by a Valkey (Redis-compatible) instance. Used in multi-replica
// deployments where the in-memory queue would lose jobs across
// restarts and prevent horizontal scaling.
//
// # Storage layout
//
// Four Valkey keys are owned by this package:
//
//   - repo-guardian:queue:jobs        LIST  — pending jobs (LPUSH/BRPOP)
//   - repo-guardian:queue:in-flight   ZSET  — claimed jobs awaiting ack;
//     score is the unix-nanos claim timestamp; member is the job JSON
//   - repo-guardian:queue:delayed     ZSET  — jobs parked until a due
//     time (IMPL-0022); score is the unix-nanos due timestamp; member
//     is the job JSON
//   - repo-guardian:lock:reaper       STRING — reaper leadership lock
//     (SET NX EX), held by exactly one pod per reap interval
//
// # Claim protocol
//
// Producers `LPUSH` JSON-encoded Jobs onto `queue:jobs`. Consumers run
// in a loop:
//
//  1. `BRPOP queue:jobs 5s` to wait for a job. The timeout makes the
//     loop responsive to context cancellation without a blocking
//     network call.
//  2. `ZADD queue:in-flight <now-nanos> <json>` to claim. From this
//     point the reaper considers the job in-flight; if the worker
//     crashes after BRPOP but before ZADD the job is lost (gap is
//     documented and acceptable per the at-least-once contract; the
//     window is microseconds in practice).
//  3. Handler runs. Success → `ZREM queue:in-flight <json>` (ack);
//     error or timeout → leave in-flight for the reaper.
//
// # Reaper
//
// One pod at a time runs the reaper, gated by the `lock:reaper` SETNX
// key. Every REAPER_INTERVAL, the leader scans
// `ZRANGEBYSCORE queue:in-flight 0 (now - JOB_ACK_TIMEOUT)`,
// re-LPUSHes each entry to `queue:jobs`, then ZREMs from in-flight.
// The Lua script `requeueScript` performs the re-LPUSH + ZREM
// atomically so the requeue is visible to a consumer at exactly the
// moment the in-flight entry disappears.
//
// # Delayed jobs
//
// EnqueueAfter parks jobs in `queue:delayed`, a ZSET scored by due
// unix-nanos (IMPL-0022). Every reap interval the leader promotes
// due entries back onto `queue:jobs` via `promoteScript`
// (ZRANGEBYSCORE + ZREM + LPUSH in one atomic script), so delivery
// lags the due time by up to one REAPER_INTERVAL. The reap interval
// deliberately does double duty — stuck-job reaping and delayed-job
// promotion — per DESIGN-0021 OQ3: one leader election, one cadence.
//
// # Job-ID determinism
//
// Enqueue rewrites Job.ID to a SHA-256 hash of
// (InstallationID, Owner, Repo) before serialising. Same triple →
// same ID, so duplicate enqueues are visible in dedupe-aware metrics.
// Engine reconcile is idempotent regardless (see INV-0003).
package valkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// Default key names. Operators can override via Options for testing
// against a shared Valkey instance.
const (
	DefaultJobsKey     = "repo-guardian:queue:jobs"
	DefaultInFlightKey = "repo-guardian:queue:in-flight"
	DefaultDelayedKey  = "repo-guardian:queue:delayed"
	DefaultReaperLock  = "repo-guardian:lock:reaper"
)

// brpopTimeout caps a single BRPOP wait so the consumer loop can
// react to context cancellation. Five seconds keeps idle Valkey RPS
// low while still feeling responsive on shutdown.
const brpopTimeout = 5 * time.Second

// recoveryTimeout caps the LPUSH used to re-enqueue a payload whose
// post-BRPOP ZADD to in-flight failed. Detached from the caller's
// ctx because the caller's cancellation is usually what triggered
// the recovery in the first place — we still need the recovery to
// succeed against Valkey.
const recoveryTimeout = 5 * time.Second

// requeueScript atomically removes a member from the in-flight ZSET
// and pushes it back onto the jobs list. KEYS[1]=in-flight,
// KEYS[2]=jobs; ARGV[1]=member.
var requeueScript = redis.NewScript(`
redis.call("ZREM", KEYS[1], ARGV[1])
redis.call("LPUSH", KEYS[2], ARGV[1])
return 1
`)

// deferScript atomically moves a job payload from the in-flight ZSET
// into the delayed ZSET, scored by its due time. KEYS[1]=in-flight,
// KEYS[2]=delayed; ARGV[1]=member to remove from in-flight,
// ARGV[2]=member to park, ARGV[3]=due unix-nanos score.
//
// Two member arguments because the worker defer path re-serialises
// the job with updated retry accounting (IMPL-0022 Phase 4 increments
// Attempts): the in-flight entry holds the original payload bytes
// while the parked entry carries the updated ones. Callers with no
// in-flight entry pass the same payload for both — the ZREM is then
// a no-op.
var deferScript = redis.NewScript(`
redis.call("ZREM", KEYS[1], ARGV[1])
redis.call("ZADD", KEYS[2], ARGV[3], ARGV[2])
return 1
`)

// promoteScript atomically moves every delayed-set entry whose score
// (due unix-nanos) is at or before now back onto the jobs list.
// KEYS[1]=delayed, KEYS[2]=jobs; ARGV[1]=now unix-nanos. Returns the
// promoted count. The whole batch moves in one script execution, so
// a member can never be observed in both keys and two racing
// promoters can never double-deliver.
var promoteScript = redis.NewScript(`
local due = redis.call("ZRANGEBYSCORE", KEYS[1], 0, ARGV[1])
for i = 1, #due do
  redis.call("ZREM", KEYS[1], due[i])
  redis.call("LPUSH", KEYS[2], due[i])
end
return #due
`)

// ErrClosed is returned by Enqueue and Subscribe after Close.
var ErrClosed = errors.New("valkey queue closed")

// Options configures a Queue instance.
type Options struct {
	// JobsKey is the Valkey LIST holding pending jobs. Empty → DefaultJobsKey.
	JobsKey string

	// InFlightKey is the Valkey ZSET tracking claimed jobs. Empty →
	// DefaultInFlightKey.
	InFlightKey string

	// DelayedKey is the Valkey ZSET parking jobs until a due time.
	// Empty → DefaultDelayedKey.
	DelayedKey string

	// ReaperLockKey is the Valkey STRING holding the reaper SETNX lock.
	// Empty → DefaultReaperLock.
	ReaperLockKey string

	// Logger receives operational logs. nil → slog.Default().
	Logger *slog.Logger
}

// Queue is the Valkey-backed implementation of queue.Queue.
type Queue struct {
	client redis.UniversalClient
	opts   Options
	logger *slog.Logger

	closeOnce sync.Once
	closed    chan struct{}
}

// applyKeyDefaults returns o with any unset Valkey key name filled
// from the package defaults. All four keys (jobs, in-flight, delayed,
// reaper lock) are constructed here and nowhere else — the DESIGN-0015
// partition hook: a partitioned deployment derives its per-partition
// key names by overriding this one spot.
func (o Options) applyKeyDefaults() Options {
	if o.JobsKey == "" {
		o.JobsKey = DefaultJobsKey
	}

	if o.InFlightKey == "" {
		o.InFlightKey = DefaultInFlightKey
	}

	if o.DelayedKey == "" {
		o.DelayedKey = DefaultDelayedKey
	}

	if o.ReaperLockKey == "" {
		o.ReaperLockKey = DefaultReaperLock
	}

	return o
}

// New constructs a Queue against the given client. The client is
// owned by the caller for testability — Close on the queue does NOT
// close the client.
func New(client redis.UniversalClient, opts Options) *Queue {
	opts = opts.applyKeyDefaults()

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Queue{
		client: client,
		opts:   opts,
		logger: logger,
		closed: make(chan struct{}),
	}
}

// Enqueue serialises j and LPUSHes it onto the jobs list. The Job ID
// is rewritten to the deterministic (installationID, owner, repo)
// hash before serialisation so duplicate enqueues are observable
// downstream. Returns ErrClosed if the queue has been closed,
// ctx.Err() on cancellation, or the underlying redis error.
//
// The Job-by-value signature is locked by the queue.Queue interface
// per DESIGN-0012; gocritic's hugeParam flag is intentional.
func (q *Queue) Enqueue(ctx context.Context, j queue.Job) error { //nolint:gocritic // interface contract
	select {
	case <-q.closed:
		return ErrClosed
	default:
	}

	j.ID = JobID(j.InstallationID, j.Owner, j.Repo)

	if j.EnqueuedAt.IsZero() {
		j.EnqueuedAt = time.Now().UTC()
	}

	payload, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("valkey.Enqueue: marshal: %w", err)
	}

	if err := q.client.LPush(ctx, q.opts.JobsKey, payload).Err(); err != nil {
		return fmt.Errorf("valkey.Enqueue: LPUSH: %w", err)
	}

	return nil
}

// EnqueueAfter parks j in the delayed ZSET to become runnable no
// earlier than at. A past-or-now at falls through to Enqueue per the
// interface contract. Promotion back onto the jobs list happens on
// the reaper leader's tick, so delivery lags at by up to one
// REAPER_INTERVAL — the contract only promises "not before at".
//
// Parking is atomic with removal of any byte-identical in-flight
// payload (deferScript) so a deferred job is never in two keys at
// once. Re-parking the same payload updates its due time — ZADD
// member semantics.
func (q *Queue) EnqueueAfter(ctx context.Context, j queue.Job, at time.Time) error { //nolint:gocritic // interface contract
	if !at.After(time.Now()) {
		return q.Enqueue(ctx, j)
	}

	select {
	case <-q.closed:
		return ErrClosed
	default:
	}

	j.ID = JobID(j.InstallationID, j.Owner, j.Repo)

	if j.EnqueuedAt.IsZero() {
		j.EnqueuedAt = time.Now().UTC()
	}

	j.AvailableAt = at.UTC()

	payload, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("valkey.EnqueueAfter: marshal: %w", err)
	}

	if err := q.deferPayload(ctx, string(payload), string(payload), at); err != nil {
		return fmt.Errorf("valkey.EnqueueAfter: %w", err)
	}

	return nil
}

// deferPayload runs deferScript: remove inFlightMember from the
// in-flight ZSET and park parked in the delayed ZSET due at. The two
// members differ when the caller re-serialised the job with updated
// retry accounting (IMPL-0022 Phase 4); external producers pass the
// same payload for both.
func (q *Queue) deferPayload(ctx context.Context, inFlightMember, parked string, at time.Time) error {
	if _, err := deferScript.Run(
		ctx,
		q.client,
		[]string{q.opts.InFlightKey, q.opts.DelayedKey},
		inFlightMember,
		parked,
		at.UnixNano(),
	).Result(); err != nil {
		return fmt.Errorf("defer script: %w", err)
	}

	return nil
}

// Subscribe runs the consumer loop until ctx is cancelled or the
// queue is closed. It calls handler once per claimed job; handler
// errors leave the job in-flight for the reaper to requeue, which
// satisfies the at-least-once contract.
func (q *Queue) Subscribe(ctx context.Context, handler func(context.Context, queue.Job) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.closed:
			return ErrClosed
		default:
		}

		payload, err := q.brpopOnce(ctx)
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}

			if errors.Is(err, context.Canceled) {
				return err
			}

			q.logger.WarnContext(ctx, "valkey BRPOP failed", "error", err)

			continue
		}

		q.processPayload(ctx, payload, handler)
	}
}

// brpopOnce performs a single BRPOP with brpopTimeout. Returns
// (payload, nil) on success, (_, redis.Nil) on timeout, or wraps the
// underlying redis error.
func (q *Queue) brpopOnce(ctx context.Context) (string, error) {
	res, err := q.client.BRPop(ctx, brpopTimeout, q.opts.JobsKey).Result()
	if err != nil {
		return "", err
	}

	// res = [key, value]; defensive against a malformed reply.
	if len(res) < 2 {
		return "", fmt.Errorf("valkey BRPOP: unexpected reply %v", res)
	}

	return res[1], nil
}

// processPayload claims, decodes, and dispatches a single job
// payload. Decode failures are dropped (no point requeueing
// undecodable garbage); handler failures leave the job in-flight.
//
// If ZADD to in-flight fails after BRPOP already removed the payload
// from queue:jobs (e.g. because the caller's ctx was cancelled
// mid-claim), the payload is LPUSHed back to queue:jobs using a
// detached context so the at-least-once contract is preserved.
func (q *Queue) processPayload(ctx context.Context, payload string, handler func(context.Context, queue.Job) error) {
	if err := q.client.ZAdd(ctx, q.opts.InFlightKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: payload,
	}).Err(); err != nil {
		q.recoverPayload(ctx, payload, err)

		return
	}

	metrics.QueueClaimedTotal.Inc()

	var j queue.Job
	if err := json.Unmarshal([]byte(payload), &j); err != nil {
		q.logger.WarnContext(ctx, "valkey decode failed; dropping job", "error", err)

		if zerr := q.client.ZRem(ctx, q.opts.InFlightKey, payload).Err(); zerr != nil {
			q.logger.WarnContext(ctx, "valkey ZREM after decode failure failed", "error", zerr)
		}

		return
	}

	if err := handler(ctx, j); err != nil {
		q.logger.WarnContext(ctx, "queue handler error; leaving in-flight for reaper",
			"job_id", j.ID,
			"owner", j.Owner,
			"repo", j.Repo,
			"error", err,
		)
		metrics.QueueAckedTotal.WithLabelValues("error").Inc()

		return
	}

	if err := q.client.ZRem(ctx, q.opts.InFlightKey, payload).Err(); err != nil {
		q.logger.WarnContext(ctx, "valkey ZREM ack failed; reaper will resurface",
			"job_id", j.ID,
			"error", err,
		)

		return
	}

	metrics.QueueAckedTotal.WithLabelValues("success").Inc()
}

// recoverPayload re-LPUSHes a payload that was BRPOPed but failed to
// claim (ZADD to in-flight failed, typically because the subscriber's
// ctx was cancelled mid-claim). Uses a detached context with a short
// timeout so the recovery succeeds even when the caller's ctx is
// already cancelled — the alternative is silently losing the job and
// violating the at-least-once contract.
//
// If the recovery LPUSH also fails the job IS lost; the log captures
// both errors so an operator can root-cause.
func (q *Queue) recoverPayload(ctx context.Context, payload string, claimErr error) {
	// Detached on purpose: the caller's ctx is usually what triggered
	// the recovery (BRPOP succeeded, then ctx-cancelled before ZADD).
	// Using ctx here would propagate the same cancellation and lose
	// the job.
	recoveryCtx, cancel := context.WithTimeout(context.Background(), recoveryTimeout)
	defer cancel()

	//nolint:contextcheck // recoveryCtx is intentionally detached from caller ctx
	rerr := q.client.LPush(recoveryCtx, q.opts.JobsKey, payload).Err()
	if rerr != nil {
		q.logger.WarnContext(ctx, "valkey ZADD failed and LPUSH recovery failed; job lost",
			"zadd_error", claimErr,
			"lpush_error", rerr,
		)

		return
	}

	q.logger.WarnContext(ctx, "valkey ZADD in-flight failed; payload re-enqueued for retry",
		"error", claimErr,
	)
}

// Close releases queue resources. The underlying redis client is NOT
// closed — the caller owns its lifecycle. Idempotent.
func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		close(q.closed)
	})

	return nil
}

// Depth returns the current pending-job count via LLEN. Useful for
// metrics scrapes; not part of the queue.Queue interface.
func (q *Queue) Depth(ctx context.Context) (int64, error) {
	n, err := q.client.LLen(ctx, q.opts.JobsKey).Result()
	if err != nil {
		return 0, fmt.Errorf("valkey.Depth: LLEN: %w", err)
	}

	return n, nil
}

// InFlight returns the current in-flight job count via ZCARD.
func (q *Queue) InFlight(ctx context.Context) (int64, error) {
	n, err := q.client.ZCard(ctx, q.opts.InFlightKey).Result()
	if err != nil {
		return 0, fmt.Errorf("valkey.InFlight: ZCARD: %w", err)
	}

	return n, nil
}

// StartDepthPoller polls Depth and InFlight every interval and writes
// the values to the registered queue_depth gauge. Returns when ctx is
// cancelled. Errors are logged at WARN — transient Valkey hiccups
// shouldn't crash observability.
func (q *Queue) StartDepthPoller(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if depth, err := q.Depth(ctx); err == nil {
				metrics.QueueDepth.WithLabelValues("jobs").Set(float64(depth))
			} else {
				q.logger.WarnContext(ctx, "queue depth poll failed", "error", err)
			}

			if inflight, err := q.InFlight(ctx); err == nil {
				metrics.QueueDepth.WithLabelValues("in-flight").Set(float64(inflight))
			} else {
				q.logger.WarnContext(ctx, "queue in-flight poll failed", "error", err)
			}
		}
	}
}

// JobID returns the deterministic SHA-256-based identifier for the
// (installationID, owner, repo) triple. Exposed so producers and
// tests can compute the same ID without round-tripping through
// Enqueue.
func JobID(installationID int64, owner, repo string) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%d/%s/%s", installationID, owner, repo))

	return hex.EncodeToString(h[:16])
}
