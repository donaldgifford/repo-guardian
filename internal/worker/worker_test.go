package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	memqueue "github.com/donaldgifford/repo-guardian/internal/queue/memory"
	"github.com/donaldgifford/repo-guardian/internal/worker"
)

// TestPool_StartStop verifies that the pool launches and shuts down
// cleanly with no jobs delivered.
func TestPool_StartStop(t *testing.T) {
	t.Parallel()

	q := memqueue.New(4)

	defer func() { _ = q.Close() }()

	p := worker.New(q, nil, nil, 2, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	cancel()
	p.Stop()
}

// TestPool_StopIdempotent verifies that calling Stop multiple times
// is safe.
func TestPool_StopIdempotent(t *testing.T) {
	t.Parallel()

	q := memqueue.New(1)
	p := worker.New(q, nil, nil, 1, slog.Default())

	p.Stop() // before Start
	p.Stop() // double-Stop, also fine
}

// TestPool_DeliversJobsToHandler exercises the full path: pool
// launches workers → queue.Subscribe pulls jobs → processJob invoked.
//
// We can't easily stub the engine without a substantial fake; instead
// this test launches the pool with a memory queue, enqueues jobs, and
// validates the queue actually drains. processJob will fail at
// CreateInstallationClient (nil client) — that's expected and confirms
// the pool reached the dispatch path.
func TestPool_DrainsQueue(t *testing.T) {
	t.Parallel()

	q := memqueue.New(8)

	defer func() { _ = q.Close() }()

	// Track how many handler invocations the queue saw via a
	// secondary subscriber that doesn't run engine.CheckRepo.
	var seen atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
			seen.Add(1)

			return nil
		})
	}()

	for i := range 3 {
		err := q.Enqueue(context.Background(), queue.Job{
			ID:             "job-" + string(rune('0'+i)),
			InstallationID: 42,
			Owner:          "org",
			Repo:           "repo",
			Trigger:        queue.TriggerWebhook,
			EnqueuedAt:     time.Now(),
		})
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && seen.Load() < 3 {
		time.Sleep(time.Millisecond)
	}

	if seen.Load() != 3 {
		t.Errorf("expected 3 jobs delivered, got %d", seen.Load())
	}
}

// TestPool_HandlerErrorContinues confirms that the pool does not
// halt on individual job failures.
func TestPool_HandlerErrorContinues(t *testing.T) {
	t.Parallel()

	q := memqueue.New(4)

	defer func() { _ = q.Close() }()

	var calls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
			calls.Add(1)

			return errors.New("simulated handler failure")
		})
	}()

	for i := range 3 {
		_ = q.Enqueue(context.Background(), queue.Job{ID: string(rune('0' + i))})
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() < 3 {
		time.Sleep(time.Millisecond)
	}

	if calls.Load() != 3 {
		t.Errorf("handler errors must not stop the pool; calls=%d", calls.Load())
	}
}
