package worker_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/worker"
)

// recordingQueue is a test-local queue.Queue stub. Subscribe blocks
// until the context is cancelled (mirroring how a real backend
// behaves when idle). Not a backend replacement — see DESIGN-0018.
type recordingQueue struct {
	mu   sync.Mutex
	jobs []queue.Job
}

func newRecordingQueue() *recordingQueue { return &recordingQueue{} }

func (r *recordingQueue) Enqueue(_ context.Context, j queue.Job) error { //nolint:gocritic // interface contract
	r.mu.Lock()
	r.jobs = append(r.jobs, j)
	r.mu.Unlock()

	return nil
}

// EnqueueAfter records like Enqueue, stamping the due-time on the
// recorded job so tests can assert deferral scheduling.
func (r *recordingQueue) EnqueueAfter(ctx context.Context, j queue.Job, at time.Time) error { //nolint:gocritic // interface contract
	j.AvailableAt = at

	return r.Enqueue(ctx, j)
}

func (*recordingQueue) Subscribe(ctx context.Context, _ func(context.Context, queue.Job) error) error {
	<-ctx.Done()

	return ctx.Err()
}

func (*recordingQueue) Close() error { return nil }

// TestPool_StartStop verifies that the pool launches and shuts down
// cleanly with no jobs delivered.
func TestPool_StartStop(t *testing.T) {
	t.Parallel()

	q := newRecordingQueue()

	defer func() { _ = q.Close() }()

	p := worker.New(q, nil, nil, nil, "", 10, 2, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	cancel()
	p.Stop()
}

// TestPool_StopIdempotent verifies that calling Stop multiple times
// is safe.
func TestPool_StopIdempotent(t *testing.T) {
	t.Parallel()

	q := newRecordingQueue()
	p := worker.New(q, nil, nil, nil, "", 10, 1, slog.Default())

	p.Stop() // before Start
	p.Stop() // double-Stop, also fine
}

// Pre-IMPL-0016 the worker test suite included two further tests
// (TestPool_DrainsQueue, TestPool_HandlerErrorContinues) that
// exercised memqueue's Subscribe pump rather than the worker pool
// itself — they spawned a goroutine calling q.Subscribe directly,
// never invoking worker.New. With memqueue removed, the equivalent
// pump-correctness behaviour is covered by the queue/valkey
// integration tests (EnqueueDequeue + CloseUnblocksSubscribe under
// the integration build tag).
