// Package valkey is the leader-elected implementation of
// scheduler.Scheduler. Each replica runs a ticker; on every tick the
// scheduler attempts `SET repo-guardian:lock:<name> <pod-id> NX EX <ttl>`.
// Only the lock holder runs the registered handler — other replicas
// skip the tick.
//
// # Lock semantics
//
// The lock is intentionally NOT extended mid-handler. If the handler
// runs longer than `ttl`, two pods may overlap on the next tick. The
// operator is responsible for keeping `ttl` larger than the worst-case
// handler runtime (sweep handler should be < 5s for a 200-repo batch).
//
// The lock is also NOT released early on success. Letting the TTL
// expire keeps recovery simple — a process death mid-handler leaves
// the next tick's election to whichever replica wakes up after the
// TTL elapses.
package valkey

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrStopped is returned by Schedule when the Scheduler has been
// stopped via Stop().
var ErrStopped = errors.New("scheduler stopped")

// Default lock TTL. Operators tune via Options.LockTTL.
const defaultLockTTL = 30 * time.Second

// Options configures a Scheduler instance.
type Options struct {
	// PodID is the SETNX value for the leader lock. Required for
	// observability so the leader pod can be identified in logs and
	// metrics.
	PodID string

	// LockKeyPrefix prefixes every Schedule's lock key. Useful for
	// running multiple schedulers against a shared Valkey instance
	// (tests). Empty → "repo-guardian:lock:".
	LockKeyPrefix string

	// LockTTL is the TTL passed to `SET ... NX EX`. Empty/zero →
	// defaultLockTTL (30s).
	LockTTL time.Duration

	// Logger receives operational logs. nil → slog.Default().
	Logger *slog.Logger
}

// Scheduler is the Valkey-backed Scheduler implementation. Construct
// via New; the zero value is not usable.
type Scheduler struct {
	client redis.UniversalClient
	opts   Options
	logger *slog.Logger

	mu       sync.Mutex
	stopped  bool
	stopOnce sync.Once
	cancels  []context.CancelFunc
	wg       sync.WaitGroup
}

// New constructs a Scheduler against the given client. The client is
// owned by the caller; Stop on the scheduler does NOT close it.
func New(client redis.UniversalClient, opts Options) *Scheduler {
	if opts.LockKeyPrefix == "" {
		opts.LockKeyPrefix = "repo-guardian:lock:"
	}

	if opts.LockTTL <= 0 {
		opts.LockTTL = defaultLockTTL
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Scheduler{
		client: client,
		opts:   opts,
		logger: logger,
	}
}

// Schedule registers handler under name and runs the ticker loop.
// On every tick, the scheduler attempts to acquire the leader lock;
// the holder runs handler, others skip. Returns ErrStopped if called
// after Stop.
//
// Unlike the ticker backend, Schedule does NOT fire immediately —
// the first invocation waits for the first tick boundary. Otherwise
// every replica would race to acquire the lock simultaneously at
// startup, which is correct but noisy in metrics.
func (s *Scheduler) Schedule(ctx context.Context, name string, interval time.Duration, handler func(context.Context) error) error {
	s.mu.Lock()

	if s.stopped {
		s.mu.Unlock()

		return ErrStopped
	}

	hctx, cancel := context.WithCancel(ctx)
	s.cancels = append(s.cancels, cancel)
	s.wg.Add(1)
	s.mu.Unlock()

	go s.run(hctx, name, interval, handler)

	return nil
}

func (s *Scheduler) run(ctx context.Context, name string, interval time.Duration, handler func(context.Context) error) {
	defer s.wg.Done()

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx, name, handler)
		}
	}
}

// tick attempts to acquire the leader lock for name and, on success,
// invokes handler. Lock acquisition failure is silent (the expected
// case for non-leader replicas). Handler errors are logged.
func (s *Scheduler) tick(ctx context.Context, name string, handler func(context.Context) error) {
	key := s.opts.LockKeyPrefix + name

	acquired, err := s.client.SetNX(ctx, key, s.opts.PodID, s.opts.LockTTL).Result()
	if err != nil {
		s.logger.WarnContext(ctx, "scheduler SETNX failed",
			"name", name,
			"key", key,
			"error", err,
		)

		return
	}

	if !acquired {
		return
	}

	s.logger.DebugContext(ctx, "scheduler tick acquired lock",
		"name", name,
		"pod_id", s.opts.PodID,
	)

	if err := handler(ctx); err != nil {
		s.logger.WarnContext(ctx, "scheduled handler error",
			"name", name,
			"pod_id", s.opts.PodID,
			"error", err,
		)
	}
}

// Stop signals every registered handler to return and waits for all
// of them to finish. Idempotent.
func (s *Scheduler) Stop() error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		cancels := s.cancels
		s.cancels = nil
		s.mu.Unlock()

		for _, c := range cancels {
			c()
		}

		s.wg.Wait()
	})

	return nil
}

// LockKeyForName returns the Valkey key used for name's leader lock.
// Exposed for integration tests that want to introspect the lock
// state without accessing unexported fields.
func (s *Scheduler) LockKeyForName(name string) string {
	return fmt.Sprintf("%s%s", s.opts.LockKeyPrefix, name)
}
