// Package queue defines the work-queue interface used by repo-guardian.
// Producers (webhook handler, scheduler sweep) call Enqueue; consumers
// (worker pool) call Subscribe to receive a stream of jobs. Delivery
// is at-least-once; deduplication is handled by the engine's idempotent
// reconcile path (see INV-0003).
//
// Implementations live in subpackages (`queue/valkey`)
// and are selected via `QUEUE_BACKEND` at runtime.
package queue

import (
	"context"
	"fmt"
	"time"
)

// Trigger values for Job.Trigger. Defined as constants so producers
// share a canonical set without magic strings.
const (
	TriggerScheduler = "scheduler"
	TriggerWebhook   = "webhook"
	TriggerPush      = "push"
)

// Job is a single unit of reconcile work. Identity is the ID — durable
// queues use it for idempotent claim/ack. Producers should generate
// IDs with enough entropy to be unique across the fleet (e.g., a UUID
// or `<owner>/<repo>/<unix-nanos>`).
//
// Jobs are serialised as JSON into the durable queue; fields added
// later (Attempts, AvailableAt) are deliberately untagged like the
// originals so payloads written by an older binary decode with zero
// values — which are the correct semantics ("never retried",
// "runnable now"). No queue drain is required on upgrade.
type Job struct {
	ID             string
	InstallationID int64
	Owner          string
	Repo           string
	Trigger        string
	EnqueuedAt     time.Time

	// Attempts counts delivery attempts that did not complete the
	// job: it is incremented on every deferral and every reaper
	// requeue. When it exceeds the configured cap the job takes the
	// terminal disposition documented on Queue.
	Attempts int

	// AvailableAt is the earliest instant the job may be delivered.
	// The zero value means "runnable now".
	AvailableAt time.Time
}

// RetryAfterError signals that a job could not proceed and must not
// be retried before After. Returning it from a Subscribe handler is
// a deliberate deferral, not a failure: the job moves to the delayed
// set with a due-time, its Attempts count is incremented, and the
// worker slot is freed immediately.
type RetryAfterError struct {
	// After is the earliest instant the job may run again.
	After time.Time

	// Reason labels why the job was deferred (e.g. "rate_limit",
	// "secondary_limit"). It is used as a metric label, so values
	// must come from a small fixed set.
	Reason string

	// Err is the optional underlying cause.
	Err error
}

func (e *RetryAfterError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("retry after %s (%s): %v", e.After.Format(time.RFC3339), e.Reason, e.Err)
	}

	return fmt.Sprintf("retry after %s (%s)", e.After.Format(time.RFC3339), e.Reason)
}

// Unwrap exposes the underlying cause to errors.Is/As chains.
func (e *RetryAfterError) Unwrap() error { return e.Err }

// Queue is the producer/consumer boundary for reconcile work.
//
// Subscribe blocks for the lifetime of ctx; the implementation invokes
// handler once per claimed job. A nil return from handler is an
// implicit ack; an error is a nack (durable implementations may
// retry; the in-memory implementation logs and drops).
//
// Close releases resources (network connections, channels). Calling
// Close while Subscribe is active must cause Subscribe to return.
type Queue interface {
	Enqueue(ctx context.Context, j Job) error
	Subscribe(ctx context.Context, handler func(context.Context, Job) error) error
	Close() error
}
