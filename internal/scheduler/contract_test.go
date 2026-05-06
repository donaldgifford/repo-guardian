// Cross-implementation contract tests for scheduler.Scheduler. The
// suite runs against the ticker backend in standard `go test`; the
// valkey backend hooks the same suite under the integration build
// tag so behavioral parity is locked in CI.
//
// Important caveat: ticker fires the handler on every replica
// independently, while valkey fires on the leader only. The
// contract tests use a single Scheduler instance so the
// "fires-once-per-tick" expectation holds for both.
package scheduler_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/scheduler"
	"github.com/donaldgifford/repo-guardian/internal/scheduler/ticker"
)

// runSchedulerContract drives the shared scheduler.Scheduler
// behaviour against a fresh instance from the constructor.
// Constructors are responsible for any per-test cleanup.
func runSchedulerContract(t *testing.T, name string, factory func(*testing.T) scheduler.Scheduler) {
	t.Helper()

	t.Run(name+"/Schedule_Fires", func(t *testing.T) {
		s := factory(t)

		var fires atomic.Int32

		ctx := t.Context()

		if err := s.Schedule(ctx, "noop", 100*time.Millisecond, func(_ context.Context) error {
			fires.Add(1)

			return nil
		}); err != nil {
			t.Fatalf("Schedule: %v", err)
		}

		// Wait for at least one tick. Ticker fires immediately on
		// startup; valkey waits for the first interval boundary.
		// Either way, 600ms covers the worst case.
		time.Sleep(600 * time.Millisecond)

		if fires.Load() == 0 {
			t.Fatalf("expected at least 1 fire, got 0")
		}

		if err := s.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})

	t.Run(name+"/Stop_Idempotent", func(t *testing.T) {
		s := factory(t)

		if err := s.Stop(); err != nil {
			t.Fatalf("first Stop: %v", err)
		}

		if err := s.Stop(); err != nil {
			t.Fatalf("second Stop: %v", err)
		}
	})

	t.Run(name+"/Schedule_AfterStop_Errors", func(t *testing.T) {
		s := factory(t)

		if err := s.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}

		err := s.Schedule(
			context.Background(),
			"after-stop",
			time.Second,
			func(_ context.Context) error { return nil },
		)
		if err == nil {
			t.Fatalf("Schedule after Stop should return ErrStopped, got nil")
		}
	})
}

// TestSchedulerContract_Ticker exercises the ticker backend against
// the shared contract suite. The valkey backend runs the same
// helper under its `_integration_test.go` file so parity is
// observable in CI.
func TestSchedulerContract_Ticker(t *testing.T) {
	runSchedulerContract(t, "ticker", func(_ *testing.T) scheduler.Scheduler {
		return ticker.New()
	})
}
