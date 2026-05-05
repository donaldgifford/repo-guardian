package valkey_test

import (
	"context"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/scheduler/valkey"
)

// TestStop_BeforeSchedule asserts Stop is idempotent and that
// Schedule returns ErrStopped after Stop.
func TestStop_BeforeSchedule(t *testing.T) {
	t.Parallel()

	s := valkey.New(nil, valkey.Options{PodID: "test"})

	if err := s.Stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}

	err := s.Schedule(context.Background(), "noop", time.Second, func(_ context.Context) error { return nil })
	if err == nil {
		t.Fatalf("Schedule after Stop should return ErrStopped, got nil")
	}
}

// TestLockKeyForName asserts the prefix is honoured. Important for
// integration tests that share a Valkey instance and need to reset
// per-test state.
func TestLockKeyForName(t *testing.T) {
	t.Parallel()

	s := valkey.New(nil, valkey.Options{PodID: "test", LockKeyPrefix: "custom:"})

	if got, want := s.LockKeyForName("sweep"), "custom:sweep"; got != want {
		t.Fatalf("LockKeyForName: got %q, want %q", got, want)
	}
}
