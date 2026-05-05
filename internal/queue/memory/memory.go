// Package memory provides a buffered-channel implementation of
// queue.Queue. Suitable for unit tests and no-dep single-replica
// deployments. Restart loses any queued jobs; webhook events arriving
// during a restart window are dropped at the HTTP layer.
package memory

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/donaldgifford/repo-guardian/internal/queue"
)

// ErrClosed is returned by Enqueue and Subscribe when the queue has
// been closed.
var ErrClosed = errors.New("queue closed")

// Queue is the buffered-channel implementation. The zero value is not
// usable; construct via New(size).
type Queue struct {
	jobs chan queue.Job

	closeOnce sync.Once
	closed    chan struct{}
}

// New returns a Queue with the given buffer size. A size of 0 yields
// an unbuffered channel — Enqueue then blocks until a Subscribe
// handler is ready, which is rarely what you want.
func New(size int) *Queue {
	return &Queue{
		jobs:   make(chan queue.Job, size),
		closed: make(chan struct{}),
	}
}

// Enqueue places j onto the queue. Returns ErrClosed if the queue has
// been closed, ctx.Err() if ctx is cancelled before the buffered slot
// becomes available, or nil on success.
//
// The Job-by-value signature is locked by the queue.Queue interface
// per DESIGN-0012; producers commonly pass struct literals at call
// sites and a *Job indirection at the boundary buys nothing.
func (q *Queue) Enqueue(ctx context.Context, j queue.Job) error { //nolint:gocritic // interface contract
	select {
	case <-q.closed:
		return ErrClosed
	default:
	}

	select {
	case q.jobs <- j:
		return nil
	case <-q.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe blocks until ctx is cancelled or the queue is closed,
// invoking handler for every job. Handler errors are logged at WARN;
// the in-memory queue does not retry (durable backends do).
func (q *Queue) Subscribe(ctx context.Context, handler func(context.Context, queue.Job) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.closed:
			return ErrClosed
		case j, ok := <-q.jobs:
			if !ok {
				return ErrClosed
			}

			if err := handler(ctx, j); err != nil {
				slog.WarnContext(ctx, "queue handler error",
					"job_id", j.ID,
					"owner", j.Owner,
					"repo", j.Repo,
					"error", err,
				)
			}
		}
	}
}

// Close releases the queue. Idempotent: subsequent calls are no-ops.
func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		close(q.closed)
	})

	return nil
}
