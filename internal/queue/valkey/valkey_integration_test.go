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
