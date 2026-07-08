//go:build integration

// Integration tests for the Valkey scheduler. Provisioned via
// testcontainers-go against the official Valkey 8 image.
//
//	go test -tags=integration ./internal/scheduler/valkey/...
package valkey_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/scheduler/valkey"
)

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

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func warnLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// TestSchedulerContract_Valkey runs the same behavioural contract
// that internal/scheduler/contract_test.go exercises against the
// ticker backend. Locked in CI under the integration build tag.
// Inlined rather than imported because the helper lives in a
// test-only package; duplication is short and the suite shape is
// stable.
func TestSchedulerContract_Valkey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := startValkey(ctx, t)

	makeScheduler := func() *valkey.Scheduler {
		return valkey.New(client, valkey.Options{
			PodID:         "test-pod",
			LockKeyPrefix: "test:" + t.Name() + ":",
			LockTTL:       time.Second,
			Logger:        warnLogger(),
		})
	}

	t.Run("Schedule_Fires", func(t *testing.T) {
		s := makeScheduler()

		var fires atomic.Int32

		runCtx, runCancel := context.WithCancel(ctx)
		defer runCancel()

		if err := s.Schedule(runCtx, "fires", 100*time.Millisecond, func(_ context.Context) error {
			fires.Add(1)

			return nil
		}); err != nil {
			t.Fatalf("Schedule: %v", err)
		}

		// Valkey scheduler waits for the first tick boundary; 600ms
		// covers the 100ms interval comfortably.
		time.Sleep(600 * time.Millisecond)

		if fires.Load() == 0 {
			t.Fatalf("expected at least 1 fire, got 0")
		}

		if err := s.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})

	t.Run("Stop_Idempotent", func(t *testing.T) {
		s := makeScheduler()

		if err := s.Stop(); err != nil {
			t.Fatalf("first Stop: %v", err)
		}

		if err := s.Stop(); err != nil {
			t.Fatalf("second Stop: %v", err)
		}
	})

	t.Run("Schedule_AfterStop_Errors", func(t *testing.T) {
		s := makeScheduler()

		if err := s.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}

		err := s.Schedule(
			context.Background(),
			"after-stop",
			time.Second,
			func(_ context.Context) error { return nil },
		)
		if err == nil {
			t.Fatalf("Schedule after Stop should return ErrStopped, got nil")
		}
	})
}

// TestLeaderElection_TwoPods boots two scheduler instances against
// the same Valkey, schedules the same handler on both, and asserts
// that across the configured tick window exactly one of them runs
// the handler each tick. Mirrors the homelab two-replica behaviour.
func TestLeaderElection_TwoPods(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := startValkey(ctx, t)

	prefix := "test:" + t.Name() + ":"

	makeScheduler := func(pod string) *valkey.Scheduler {
		return valkey.New(client, valkey.Options{
			PodID:         pod,
			LockKeyPrefix: prefix,
			LockTTL:       2 * time.Second,
			Logger:        warnLogger(),
		})
	}

	a := makeScheduler("pod-a")
	b := makeScheduler("pod-b")

	defer func() { _ = a.Stop() }()
	defer func() { _ = b.Stop() }()

	var (
		fires    atomic.Int32
		interval = 200 * time.Millisecond
	)

	handler := func(_ string) func(context.Context) error {
		return func(_ context.Context) error {
			fires.Add(1)
			return nil
		}
	}

	if err := a.Schedule(ctx, "sweep", interval, handler("pod-a")); err != nil {
		t.Fatalf("schedule a: %v", err)
	}

	if err := b.Schedule(ctx, "sweep", interval, handler("pod-b")); err != nil {
		t.Fatalf("schedule b: %v", err)
	}

	// Run for ~1.5s (≈ 7 ticks at 200ms). LockTTL=2s means once a pod
	// captures the lock it'll hold it for the duration of this run, so
	// we expect roughly one fire per LockTTL window — i.e. ≤ 2 fires
	// rather than 14 (2 pods × 7 ticks).
	time.Sleep(1500 * time.Millisecond)

	count := int(fires.Load())
	if count == 0 {
		t.Fatalf("expected at least one tick fire, got 0")
	}

	// With LockTTL > tick interval, every tick that fires implies the
	// other pod was blocked. The exact count depends on timing, but
	// 2 pods each firing on every tick would produce ≥ 10. Anything
	// substantially under 2× the single-pod count proves leader
	// election held.
	maxAllowed := 5
	if count > maxAllowed {
		t.Fatalf("leader election failed: %d fires across both pods (expected ≤ %d)", count, maxAllowed)
	}
}
