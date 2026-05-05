// Package queue contract tests assert behaviour that every Queue
// implementation must satisfy. The suite is parameterised by a
// constructor; queue/memory invokes it directly, queue/valkey hooks
// in under the integration build tag.
//
// Keep these tests dependency-free — anything that needs a server
// belongs in the implementation's own `_test.go` file.
package queue_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	memqueue "github.com/donaldgifford/repo-guardian/internal/queue/memory"
)

// runContract drives the shared queue.Queue contract against a fresh
// queue from the constructor. Constructors are responsible for any
// per-test cleanup.
func runContract(t *testing.T, name string, factory func(*testing.T) queue.Queue) {
	t.Helper()

	t.Run(name+"/EnqueueDequeue", func(t *testing.T) {
		q := factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		defer cancel()

		if err := q.Enqueue(ctx, queue.Job{Owner: "o", Repo: "r"}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}

		got := make(chan queue.Job, 1)

		go func() {
			_ = q.Subscribe(ctx, func(_ context.Context, j queue.Job) error {
				got <- j

				return nil
			})
		}()

		select {
		case j := <-got:
			if j.Owner != "o" || j.Repo != "r" {
				t.Fatalf("unexpected job: %+v", j)
			}
		case <-ctx.Done():
			t.Fatalf("did not receive job within timeout")
		}
	})

	t.Run(name+"/CloseUnblocksSubscribe", func(t *testing.T) {
		q := factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()

		done := make(chan struct{})

		go func() {
			defer close(done)
			_ = q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
				return nil
			})
		}()

		// Give Subscribe a moment to enter its blocking call.
		time.Sleep(50 * time.Millisecond)

		if err := q.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}

		select {
		case <-done:
		case <-ctx.Done():
			t.Fatalf("Subscribe did not return after Close within timeout")
		}
	})

	t.Run(name+"/SubscribeContextCancel", func(t *testing.T) {
		q := factory(t)
		ctx, cancel := context.WithCancel(context.Background())

		var calls atomic.Int32

		done := make(chan error, 1)

		go func() {
			done <- q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
				calls.Add(1)

				return nil
			})
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("Subscribe did not return after context cancel")
		}
	})
}

func TestContract_Memory(t *testing.T) {
	runContract(t, "memory", func(_ *testing.T) queue.Queue {
		return memqueue.New(8)
	})
}
