package ticker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/scheduler/ticker"
)

func TestSchedule_FiresImmediatelyAndOnInterval(t *testing.T) {
	t.Parallel()

	s := ticker.New()

	var calls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Schedule(ctx, "test", 20*time.Millisecond, func(_ context.Context) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && calls.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}

	if got := calls.Load(); got < 3 {
		t.Errorf("expected at least 3 fires (immediate + 2 ticks), got %d", got)
	}

	_ = s.Stop()
}

func TestSchedule_HandlerErrorContinuesLoop(t *testing.T) {
	t.Parallel()

	s := ticker.New()

	var calls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Schedule(ctx, "test", 20*time.Millisecond, func(_ context.Context) error {
		calls.Add(1)
		return errors.New("handler boom")
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && calls.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}

	if got := calls.Load(); got < 3 {
		t.Errorf("handler errors must not stop the ticker; calls=%d", got)
	}

	_ = s.Stop()
}

func TestStop_HaltsAllHandlers(t *testing.T) {
	t.Parallel()

	s := ticker.New()

	var calls atomic.Int32

	if err := s.Schedule(context.Background(), "fast", 5*time.Millisecond, func(_ context.Context) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	time.Sleep(40 * time.Millisecond)

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	before := calls.Load()
	time.Sleep(40 * time.Millisecond)

	if after := calls.Load(); after != before {
		t.Errorf("handler kept firing after Stop: before=%d after=%d", before, after)
	}
}

func TestSchedule_AfterStop_ReturnsErrStopped(t *testing.T) {
	t.Parallel()

	s := ticker.New()
	_ = s.Stop()

	err := s.Schedule(context.Background(), "x", time.Second, func(_ context.Context) error { return nil })
	if !errors.Is(err, ticker.ErrStopped) {
		t.Errorf("expected ErrStopped, got %v", err)
	}
}

func TestStop_Idempotent(t *testing.T) {
	t.Parallel()

	s := ticker.New()
	_ = s.Stop()

	if err := s.Stop(); err != nil {
		t.Errorf("second Stop should be no-op, got %v", err)
	}
}

func TestSchedule_MultipleHandlers(t *testing.T) {
	t.Parallel()

	s := ticker.New()

	var a, b atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = s.Schedule(ctx, "a", 10*time.Millisecond, func(_ context.Context) error { a.Add(1); return nil })
	_ = s.Schedule(ctx, "b", 10*time.Millisecond, func(_ context.Context) error { b.Add(1); return nil })

	time.Sleep(80 * time.Millisecond)

	if a.Load() < 2 || b.Load() < 2 {
		t.Errorf("expected both handlers to fire multiple times: a=%d b=%d", a.Load(), b.Load())
	}

	_ = s.Stop()
}
