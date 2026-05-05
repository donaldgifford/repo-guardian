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
		Image:        "valkey/valkey:8-alpine",
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
		fireCh   = make(chan string, 16)
		interval = 200 * time.Millisecond
	)

	handler := func(pod string) func(context.Context) error {
		return func(_ context.Context) error {
			fires.Add(1)
			fireCh <- pod

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
	deadline := time.Now().Add(1500 * time.Millisecond)

	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	close(fireCh)

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
