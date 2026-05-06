// Package ticker is a time.Ticker-backed implementation of
// scheduler.Scheduler. Suitable for tests and no-dep single-replica
// deployments. Each replica's ticker fires independently — must not
// be paired with multi-replica deployments without external
// coordination (use scheduler/valkey for that).
package ticker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// ErrStopped is returned by Schedule when the Scheduler has been
// stopped via Stop().
var ErrStopped = errors.New("scheduler stopped")

// Scheduler is the time.Ticker-backed implementation. The zero value
// is not usable; construct via New().
type Scheduler struct {
	mu       sync.Mutex
	stopped  bool
	stopOnce sync.Once
	cancels  []context.CancelFunc
	wg       sync.WaitGroup
}

// New returns a fresh Scheduler.
func New() *Scheduler {
	return &Scheduler{}
}

// Schedule registers handler under name and runs it every interval
// in a background goroutine, starting with an immediate fire and then
// every interval thereafter, until ctx is cancelled or Stop is called.
// Returns ErrStopped if called after Stop.
//
// Handler errors are logged and tolerated — the loop continues. This
// matches the legacy Sweeper behavior.
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

	fire := func() {
		if err := handler(ctx); err != nil {
			slog.WarnContext(ctx, "scheduled handler error",
				"name", name,
				"interval", interval,
				"error", err,
			)
		}
	}

	// Run once immediately on schedule, mirroring Sweeper.Start.
	fire()

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fire()
		}
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
