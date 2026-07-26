// Package scheduler provides the periodic-work abstraction used to
// drive repo-guardian's background loops.
//
// It contains two things:
//   - Scheduler, the interface main.go schedules handlers on, with a
//     leader-elected Valkey implementation in scheduler/valkey.
//   - Discoverer, which enumerates installations and their repositories
//     and seeds persistent repo_state rows for the stale sweeper
//     (IMPL-0015 Phase 1).
//
// The legacy Sweeper — a concrete for-select loop that enumerated every
// repository on every tick and enqueued it directly — was removed in
// IMPL-0021. Discovery (this package) and staleness-driven enqueueing
// (checker.StaleSweeper) replaced it.
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
