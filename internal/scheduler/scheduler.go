package scheduler

import (
	"context"
	"time"
)

// Scheduler runs periodic handlers on a configurable interval. The
// abstraction exists to swap the single-replica `time.Ticker` impl
// (`scheduler/ticker`) for the cluster-coordinated Valkey impl
// (`scheduler/valkey`, IMPL-0011 Phase 4) without touching call sites.
//
// Schedule registers a named handler that fires every interval until
// either ctx is cancelled or Stop is called. Multiple Schedule calls
// register independent handlers. The implementation determines whether
// a tick fires on every replica (ticker) or only on a leader (valkey).
//
// Stop releases all timers and waits for in-flight handlers to return.
// Idempotent.
type Scheduler interface {
	Schedule(ctx context.Context, name string, interval time.Duration, handler func(context.Context) error) error
	Stop() error
}
