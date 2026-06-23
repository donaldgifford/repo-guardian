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
// queues use it for idempotent claim/ack; the in-memory queue uses it
// for logging and metrics. Producers should generate IDs with enough
// entropy to be unique across the fleet (e.g., a UUID or
// `<owner>/<repo>/<unix-nanos>`).
type Job struct {
	ID             string
	InstallationID int64
	Owner          string
	Repo           string
	Trigger        string
	EnqueuedAt     time.Time
}

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
