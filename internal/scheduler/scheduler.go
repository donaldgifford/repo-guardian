package scheduler

import (
	"context"
	"time"
)

// Scheduler runs periodic handlers on a configurable interval.
// `scheduler/valkey` (IMPL-0011 Phase 4) is the only implementation;
// it fires on the leader replica only via SETNX-backed locks. The
// interface is retained so future single-replica or alternative
// implementations can be slotted in without touching call sites.
//
// Schedule registers a named handler that fires every interval until
// either ctx is cancelled or Stop is called. Multiple Schedule calls
// register independent handlers.
//
// Stop releases all timers and waits for in-flight handlers to return.
// Idempotent.
type Scheduler interface {
	Schedule(ctx context.Context, name string, interval time.Duration, handler func(context.Context) error) error
	Stop() error
}
