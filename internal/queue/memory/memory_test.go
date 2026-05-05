package memory_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/queue/memory"
)

func TestEnqueue_Subscribe_RoundTrip(t *testing.T) {
	t.Parallel()

	q := memory.New(4)

	defer func() { _ = q.Close() }()

	var (
		wg   sync.WaitGroup
		seen []string
		mu   sync.Mutex
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wg.Add(1)

	go func() {
		defer wg.Done()
		_ = q.Subscribe(ctx, func(_ context.Context, j queue.Job) error {
			mu.Lock()
			seen = append(seen, j.ID)
			mu.Unlock()

			return nil
		})
	}()

	for _, id := range []string{"a", "b", "c"} {
		if err := q.Enqueue(context.Background(), queue.Job{ID: id, Trigger: queue.TriggerWebhook}); err != nil {
			t.Fatalf("Enqueue %s: %v", id, err)
		}
	}

	deadline := time.Now().Add(time.Second)

	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()

		if n == 3 {
			break
		}

		time.Sleep(time.Millisecond)
	}

	cancel()
	wg.Wait()

	if len(seen) != 3 {
		t.Fatalf("expected 3 jobs delivered, got %d (%v)", len(seen), seen)
	}
}

func TestEnqueue_OrderPreserved(t *testing.T) {
	t.Parallel()

	q := memory.New(8)

	defer func() { _ = q.Close() }()

	for i := range 5 {
		if err := q.Enqueue(context.Background(), queue.Job{ID: string(rune('a' + i))}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var (
		got []string
		mu  sync.Mutex
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		_ = q.Subscribe(ctx, func(_ context.Context, j queue.Job) error {
			mu.Lock()
			got = append(got, j.ID)

			if len(got) == 5 {
				close(done)
			}

			mu.Unlock()

			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for 5 deliveries")
	}

	cancel()

	want := []string{"a", "b", "c", "d", "e"}
	for i, id := range want {
		if got[i] != id {
			t.Errorf("delivery order: got[%d]=%q want %q", i, got[i], id)
		}
	}
}

func TestEnqueue_HandlerErrorContinues(t *testing.T) {
	t.Parallel()

	q := memory.New(4)

	defer func() { _ = q.Close() }()

	var delivered atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
			delivered.Add(1)

			return errors.New("simulated handler failure")
		})
	}()

	for i := range 3 {
		_ = q.Enqueue(context.Background(), queue.Job{ID: string(rune('0' + i))})
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && delivered.Load() < 3 {
		time.Sleep(time.Millisecond)
	}

	if delivered.Load() != 3 {
		t.Errorf("handler errors must not stop the loop; delivered=%d", delivered.Load())
	}
}

func TestEnqueue_AfterClose_ReturnsErrClosed(t *testing.T) {
	t.Parallel()

	q := memory.New(2)
	_ = q.Close()

	err := q.Enqueue(context.Background(), queue.Job{ID: "x"})
	if !errors.Is(err, memory.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

func TestSubscribe_ReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	q := memory.New(2)

	defer func() { _ = q.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- q.Subscribe(ctx, func(_ context.Context, _ queue.Job) error {
			return nil
		})
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Subscribe did not return on context cancel")
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	if err := memory.New(1).Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
}

func TestSubscribe_AfterClose_ReturnsErrClosed(t *testing.T) {
	t.Parallel()

	q := memory.New(1)
	_ = q.Close()

	err := q.Subscribe(context.Background(), func(_ context.Context, _ queue.Job) error {
		return nil
	})
	if !errors.Is(err, memory.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}

func TestEnqueue_BlockedThenClose_ReturnsErrClosed(t *testing.T) {
	t.Parallel()

	q := memory.New(1)
	_ = q.Enqueue(context.Background(), queue.Job{ID: "filler"}) // saturate the buffer

	done := make(chan error, 1)

	go func() {
		done <- q.Enqueue(context.Background(), queue.Job{ID: "blocked"})
	}()

	// Give the goroutine time to enter the blocking select branch.
	time.Sleep(10 * time.Millisecond)
	_ = q.Close()

	select {
	case err := <-done:
		if !errors.Is(err, memory.ErrClosed) {
			t.Errorf("expected ErrClosed after close-while-blocked, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Enqueue did not unblock on close")
	}
}

func TestEnqueue_ContextCancelledMidBlock(t *testing.T) {
	t.Parallel()

	q := memory.New(1)

	defer func() { _ = q.Close() }()

	_ = q.Enqueue(context.Background(), queue.Job{ID: "filler"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- q.Enqueue(ctx, queue.Job{ID: "blocked"})
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Enqueue did not unblock on context cancel")
	}
}
