package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
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

// deliverOnceQueue hands the Subscribe handler exactly one crafted
// job and captures its return value — the minimal per-file recorder
// for exercising processJob outcomes (DESIGN-0018 convention).
type deliverOnceQueue struct {
	job    queue.Job
	result chan error
}

func (*deliverOnceQueue) Enqueue(context.Context, queue.Job) error { return nil }

func (*deliverOnceQueue) EnqueueAfter(context.Context, queue.Job, time.Time) error { return nil }

func (d *deliverOnceQueue) Subscribe(ctx context.Context, h func(context.Context, queue.Job) error) error {
	d.result <- h(ctx, d.job)

	return nil
}

func (*deliverOnceQueue) Close() error { return nil }

var errUnimplemented = errors.New("not implemented in capturingStore")

// capturingStore records UpdateRepoState calls; every other Store
// method is a no-op.
type capturingStore struct {
	mu     sync.Mutex
	states []store.RepoState
}

func (*capturingStore) GetRepoState(context.Context, int64, string, string) (*store.RepoState, error) {
	return nil, errUnimplemented
}

func (c *capturingStore) UpdateRepoState(_ context.Context, s *store.RepoState) error {
	c.mu.Lock()
	c.states = append(c.states, *s)
	c.mu.Unlock()

	return nil
}

func (*capturingStore) UpsertIfMissing(context.Context, *store.RepoState) (bool, error) {
	return false, nil
}

func (*capturingStore) StaleRepos(context.Context, time.Duration, string, int) ([]store.RepoState, error) {
	return nil, nil
}

func (*capturingStore) Close() error { return nil }

// TestPool_AttemptCap_TerminalDisposition locks IMPL-0022 task 4.4:
// a job delivered at the MAX_JOB_ATTEMPTS cap is dropped with the
// terminal disposition — exactly one StatusError repo_state write
// with a descriptive LastError, an exhausted-counter increment, and
// a nil handler return so the queue acks (drops) rather than
// retrying. The nil engine/ghClient prove the job is refused before
// any processing is attempted.
func TestPool_AttemptCap_TerminalDisposition(t *testing.T) {
	// Not parallel: reads a package-global metric after Reset.
	metrics.QueueAttemptsExhaustedTotal.Reset()

	q := &deliverOnceQueue{
		job: queue.Job{
			ID:             "cap",
			InstallationID: 7,
			Owner:          "o",
			Repo:           "r",
			Trigger:        queue.TriggerScheduler,
			Attempts:       10,
		},
		result: make(chan error, 1),
	}
	st := &capturingStore{}
	p := worker.New(q, nil, nil, st, "pv1", 10, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	var res error
	select {
	case res = <-q.result:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	if res != nil {
		t.Errorf("handler returned %v, want nil (ack-and-drop terminal disposition)", res)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.states) != 1 {
		t.Fatalf("UpdateRepoState calls = %d, want exactly 1 (terminal disposition happens once)", len(st.states))
	}

	got := st.states[0]
	if got.LastCheckStatus != store.StatusError {
		t.Errorf("LastCheckStatus = %q, want %q", got.LastCheckStatus, store.StatusError)
	}

	if !strings.Contains(got.LastError, "MAX_JOB_ATTEMPTS") {
		t.Errorf("LastError = %q, want it to name MAX_JOB_ATTEMPTS", got.LastError)
	}

	if got.InstallationID != 7 || got.Owner != "o" || got.Repo != "r" {
		t.Errorf("repo_state row = %+v, want installation 7 o/r", got)
	}

	if v := testutil.ToFloat64(metrics.QueueAttemptsExhaustedTotal.WithLabelValues("7")); v != 1 {
		t.Errorf("queue_attempts_exhausted_total{installation_id=7} = %v, want 1", v)
	}
}

// Pre-IMPL-0016 the worker test suite included two further tests
// (TestPool_DrainsQueue, TestPool_HandlerErrorContinues) that
// exercised memqueue's Subscribe pump rather than the worker pool
// itself — they spawned a goroutine calling q.Subscribe directly,
// never invoking worker.New. With memqueue removed, the equivalent
// pump-correctness behaviour is covered by the queue/valkey
// integration tests (EnqueueDequeue + CloseUnblocksSubscribe under
// the integration build tag).
