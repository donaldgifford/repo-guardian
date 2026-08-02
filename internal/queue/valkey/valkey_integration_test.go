//go:build integration

// Integration tests for the Valkey queue. Provisioned via
// testcontainers-go against the official Valkey 8 image.
//
//	go test -tags=integration ./internal/queue/valkey/...
package valkey_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/queue/valkey"
)

// startValkey provisions a Valkey 8 container and returns a connected
// redis client + cleanup hook. We pin the image tag so the test
// matrix doesn't surprise us when upstream cuts a new minor.
func startValkey(ctx context.Context, t *testing.T) *redis.Client {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:9.1-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate valkey: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}

	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})

	t.Cleanup(func() {
		_ = client.Close()
	})

	return client
}

// newTestQueue returns a Queue with logger swallowed and a clean
// keyspace per test. The keyspace prefix is `test:<name>:` so
// concurrent tests don't trip over each other's BRPOPs.
func newTestQueue(t *testing.T, client *redis.Client) *valkey.Queue {
	t.Helper()

	prefix := "test:" + t.Name()

	return valkey.New(client, valkey.Options{
		JobsKey:       prefix + ":jobs",
		InFlightKey:   prefix + ":in-flight",
		DelayedKey:    prefix + ":delayed",
		ReaperLockKey: prefix + ":lock:reaper",
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
}

// TestContract_Valkey runs the cross-backend contract suite against a
// freshly provisioned Valkey container. Mirrors the memory backend's
// TestContract_Memory in internal/queue/contract_test.go so the two
// implementations stay behaviourally identical.
func TestContract_Valkey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)

	t.Run("EnqueueDequeue", func(t *testing.T) {
		q := newTestQueue(t, client)
		runCtx, runCancel := context.WithTimeout(context.Background(), 10*time.Second)

		defer runCancel()

		if err := q.Enqueue(runCtx, queue.Job{Owner: "o", Repo: "r"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		got := make(chan queue.Job, 1)

		go func() {
			_ = q.Subscribe(runCtx, func(_ context.Context, j queue.Job) error {
				got <- j

				return nil
			})
		}()

		select {
		case j := <-got:
			if j.Owner != "o" || j.Repo != "r" {
				t.Fatalf("unexpected job: %+v", j)
			}
		case <-runCtx.Done():
			t.Fatalf("did not receive job within timeout")
		}
	})

	t.Run("SubscribeContextCancel", func(t *testing.T) {
		q := newTestQueue(t, client)
		runCtx, runCancel := context.WithCancel(context.Background())

		done := make(chan error, 1)

		go func() {
			done <- q.Subscribe(runCtx, func(_ context.Context, _ queue.Job) error {
				return nil
			})
		}()

		time.Sleep(100 * time.Millisecond)
		runCancel()

		select {
		case <-done:
		case <-time.After(brpopWait):
			t.Fatalf("Subscribe did not return after context cancel within %s", brpopWait)
		}
	})

	t.Run("CloseUnblocksSubscribe", func(t *testing.T) {
		q := newTestQueue(t, client)
		runCtx, runCancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer runCancel()

		done := make(chan struct{})

		go func() {
			defer close(done)
			_ = q.Subscribe(runCtx, func(_ context.Context, _ queue.Job) error {
				return nil
			})
		}()

		time.Sleep(100 * time.Millisecond)

		if err := q.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		select {
		case <-done:
		case <-time.After(brpopWait):
			t.Fatalf("Subscribe did not return after Close within %s", brpopWait)
		}
	})
}

// brpopWait is the upper bound on how long Subscribe takes to react
// to context cancellation in the worst case (BRPOP blocks for the
// full brpopTimeout in valkey.go).
const brpopWait = 10 * time.Second

func TestValkey_FIFOOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)

	for i := range 5 {
		if err := q.Enqueue(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	got := make([]string, 0, 5)

	var mu sync.Mutex

	go func() {
		_ = q.Subscribe(subCtx, func(_ context.Context, j queue.Job) error {
			mu.Lock()
			got = append(got, j.Repo)
			done := len(got) == 5
			mu.Unlock()

			if done {
				subCancel()
			}

			return nil
		})
	}()

	<-subCtx.Done()

	want := []string{"r0", "r1", "r2", "r3", "r4"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("FIFO ordering broken: got %v, want %v", got, want)
		}
	}
}

func TestValkey_ReaperRequeues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)

	if err := q.Enqueue(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: "stuck"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	handlerCalls := atomic.Int32{}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	go func() {
		_ = q.Subscribe(subCtx, func(_ context.Context, _ queue.Job) error {
			handlerCalls.Add(1)
			return errors.New("simulated worker crash") // leave in-flight
		})
	}()

	// Give the worker a moment to claim the job and ZADD it to in-flight.
	time.Sleep(2 * time.Second)
	subCancel()

	// Reap immediately — JobAckTimeout is short so the in-flight entry
	// is already past its deadline.
	reaper := valkey.NewReaper(q, valkey.ReaperOptions{
		PodID:         "test",
		Interval:      time.Second,
		JobAckTimeout: 100 * time.Millisecond,
		LockTTL:       5 * time.Second,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})

	reapCtx, reapCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reapCancel()

	go func() { _ = reaper.Start(reapCtx) }()

	// Now subscribe again and confirm the requeued job re-arrives.
	subCtx2, subCancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer subCancel2()

	got := make(chan string, 1)

	go func() {
		_ = q.Subscribe(subCtx2, func(_ context.Context, j queue.Job) error {
			got <- j.Repo
			return nil
		})
	}()

	select {
	case repo := <-got:
		if repo != "stuck" {
			t.Fatalf("reaper requeued wrong job: %q", repo)
		}
	case <-subCtx2.Done():
		t.Fatalf("reaper did not requeue stuck job within 10s; handler calls: %d", handlerCalls.Load())
	}
}

// fastReaper starts a reaper with ticks fast enough that promotion
// cadence never dominates a test's timeline. LockTTL is shorter than
// the interval on purpose: reapOnce never releases its lock early, so
// a TTL longer than the tick would make every second tick a no-op.
func fastReaper(t *testing.T, ctx context.Context, q *valkey.Queue, podID string) {
	t.Helper()

	reaper := valkey.NewReaper(q, valkey.ReaperOptions{
		PodID:         podID,
		Interval:      250 * time.Millisecond,
		JobAckTimeout: time.Minute,
		LockTTL:       100 * time.Millisecond,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})

	go func() { _ = reaper.Start(ctx) }()
}

// keyCounts snapshots how many members each of the three job-holding
// keys contains. The reaper lock is a STRING and never holds a job.
type keyCounts struct {
	jobs     int64
	inFlight int64
	delayed  int64
}

func countKeys(ctx context.Context, t *testing.T, client *redis.Client, prefix string) keyCounts {
	t.Helper()

	return keyCounts{
		jobs:     client.LLen(ctx, prefix+":jobs").Val(),
		inFlight: client.ZCard(ctx, prefix+":in-flight").Val(),
		delayed:  client.ZCard(ctx, prefix+":delayed").Val(),
	}
}

// waitForCounts polls until the keyspace matches want or the deadline
// passes. Polling (rather than continuous assertion) respects the
// documented microsecond BRPOP→ZADD claim gap.
func waitForCounts(ctx context.Context, t *testing.T, client *redis.Client, prefix string, want keyCounts, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	var got keyCounts

	for time.Now().Before(deadline) {
		got = countKeys(ctx, t, client, prefix)
		if got == want {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("keyspace never reached %+v within %s; last seen %+v", want, timeout, got)
}

// TestValkey_DeferredJobNotDeliveredEarly locks the IMPL-0022 task
// 2.5 contract: a job parked via EnqueueAfter is never delivered
// before its due time, and is delivered after it (within one
// promotion tick).
func TestValkey_DeferredJobNotDeliveredEarly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)

	due := time.Now().Add(2 * time.Second)
	if err := q.EnqueueAfter(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: "deferred"}, due); err != nil {
		t.Fatalf("EnqueueAfter: %v", err)
	}

	reapCtx, reapCancel := context.WithCancel(ctx)
	defer reapCancel()
	fastReaper(t, reapCtx, q, "test")

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	type delivery struct {
		at  time.Time
		job queue.Job
	}

	delivered := make(chan delivery, 1)

	go func() {
		_ = q.Subscribe(subCtx, func(_ context.Context, j queue.Job) error {
			delivered <- delivery{at: time.Now(), job: j}

			return nil
		})
	}()

	select {
	case d := <-delivered:
		if d.at.Before(due) {
			t.Fatalf("job delivered at %s, %s before due time %s",
				d.at.Format(time.RFC3339Nano), due.Sub(d.at), due.Format(time.RFC3339Nano))
		}

		if !d.job.AvailableAt.Equal(due.UTC()) {
			t.Errorf("delivered AvailableAt = %v, want %v", d.job.AvailableAt, due.UTC())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("deferred job never delivered within 15s of enqueue (due %s)", due.Format(time.RFC3339Nano))
	}
}

// TestValkey_ExactlyOneKeyInvariant walks a deferred job through its
// full lifecycle and asserts it occupies exactly one of jobs,
// in-flight, delayed at each checkpoint (IMPL-0022 task 2.6).
func TestValkey_ExactlyOneKeyInvariant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)
	prefix := "test:" + t.Name()

	// Checkpoint 1: parked — delayed only.
	due := time.Now().Add(time.Second)
	if err := q.EnqueueAfter(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: "lifecycle"}, due); err != nil {
		t.Fatalf("EnqueueAfter: %v", err)
	}

	if got := countKeys(ctx, t, client, prefix); got != (keyCounts{delayed: 1}) {
		t.Fatalf("after EnqueueAfter: %+v, want delayed only", got)
	}

	// Checkpoint 2: promoted — jobs only. No subscriber yet, so the
	// promoted entry stays observable on the list.
	reapCtx, reapCancel := context.WithCancel(ctx)
	defer reapCancel()
	fastReaper(t, reapCtx, q, "test")
	waitForCounts(ctx, t, client, prefix, keyCounts{jobs: 1}, 10*time.Second)

	// Checkpoint 3: claimed — in-flight only, held there by a handler
	// that blocks until released.
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	release := make(chan struct{})
	claimed := make(chan struct{}, 1)

	go func() {
		_ = q.Subscribe(subCtx, func(_ context.Context, _ queue.Job) error {
			claimed <- struct{}{}
			<-release

			return nil
		})
	}()

	select {
	case <-claimed:
	case <-time.After(10 * time.Second):
		t.Fatalf("job never claimed by subscriber")
	}

	if got := countKeys(ctx, t, client, prefix); got != (keyCounts{inFlight: 1}) {
		t.Fatalf("while handler running: %+v, want in-flight only", got)
	}

	// Checkpoint 4: acked — gone from all three.
	close(release)
	waitForCounts(ctx, t, client, prefix, keyCounts{}, 10*time.Second)
}

// TestValkey_PromotionLeaderGated runs two reapers against the same
// lock and asserts every parked job is delivered exactly once — the
// IMPL-0022 task 2.7 invariant that promotion is not duplicated
// across replicas.
func TestValkey_PromotionLeaderGated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)
	prefix := "test:" + t.Name()

	const parked = 5

	due := time.Now().Add(time.Second)

	for i := range parked {
		if err := q.EnqueueAfter(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: fmt.Sprintf("r%d", i)}, due); err != nil {
			t.Fatalf("EnqueueAfter %d: %v", i, err)
		}
	}

	reapCtx, reapCancel := context.WithCancel(ctx)
	defer reapCancel()
	fastReaper(t, reapCtx, q, "pod-a")
	fastReaper(t, reapCtx, q, "pod-b")

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	var (
		mu        sync.Mutex
		delivered = make(map[string]int)
		count     atomic.Int32
	)

	all := make(chan struct{}, 1)

	go func() {
		_ = q.Subscribe(subCtx, func(_ context.Context, j queue.Job) error {
			mu.Lock()
			delivered[j.Repo]++
			mu.Unlock()

			if int(count.Add(1)) == parked {
				all <- struct{}{}
			}

			return nil
		})
	}()

	select {
	case <-all:
	case <-time.After(15 * time.Second):
		t.Fatalf("only %d/%d parked jobs delivered within 15s", count.Load(), parked)
	}

	// Linger past several promotion ticks to catch a double-delivery,
	// then verify counts and an empty keyspace.
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(delivered) != parked {
		t.Fatalf("expected %d unique jobs delivered, got %d: %v", parked, len(delivered), delivered)
	}

	for repo, n := range delivered {
		if n != 1 {
			t.Fatalf("job %s delivered %d times, expected exactly 1", repo, n)
		}
	}

	if got := countKeys(ctx, t, client, prefix); got != (keyCounts{}) {
		t.Fatalf("keyspace not empty after all deliveries: %+v", got)
	}
}

func TestValkey_NoDoubleClaim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := startValkey(ctx, t)
	q := newTestQueue(t, client)

	const (
		jobs    = 200
		workers = 10
	)

	for i := range jobs {
		if err := q.Enqueue(ctx, queue.Job{InstallationID: 1, Owner: "o", Repo: fmt.Sprintf("r%d", i)}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	subCtx, subCancel := context.WithTimeout(ctx, 30*time.Second)
	defer subCancel()

	var (
		mu      sync.Mutex
		claimed = make(map[string]int)
		count   atomic.Int32
		wg      sync.WaitGroup
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_ = q.Subscribe(subCtx, func(_ context.Context, j queue.Job) error {
				mu.Lock()
				claimed[j.Repo]++
				mu.Unlock()

				if int(count.Add(1)) >= jobs {
					subCancel()
				}

				return nil
			})
		}()
	}

	wg.Wait()

	if len(claimed) != jobs {
		t.Fatalf("expected %d unique jobs claimed, got %d", jobs, len(claimed))
	}

	for repo, n := range claimed {
		if n != 1 {
			t.Fatalf("job %s claimed %d times, expected exactly 1", repo, n)
		}
	}
}
