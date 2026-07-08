//go:build integration

// End-to-end multi-replica integration test for IMPL-0011 Phase 7.
// Boots testcontainers Postgres + Valkey, runs N=10 worker
// goroutines against a shared queue, enqueues 1000 jobs with a
// fake handler, and asserts every job was processed exactly once
// and `repo_state` reflects every (installation, owner, repo) tuple.
//
//	go test -tags=integration ./internal/checker/...
package checker_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/queue"
	valkeyqueue "github.com/donaldgifford/repo-guardian/internal/queue/valkey"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

func startPostgresMR(ctx context.Context, t *testing.T) string {
	t.Helper()

	c, err := tcpostgres.Run(
		ctx,
		"postgres:18.4-alpine",
		tcpostgres.WithDatabase("repoguardian_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}

	return dsn
}

func startValkeyMR(ctx context.Context, t *testing.T) *redis.Client {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "valkey/valkey:9.1-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}

	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}

	port, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestMultiReplica_ExactlyOnce asserts the durable backends deliver
// the multi-replica promise: 10 worker goroutines drain 1000 jobs
// from a shared Valkey queue and write to a shared Postgres store,
// every job processed exactly once.
func TestMultiReplica_ExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dsn := startPostgresMR(ctx, t)
	rclient := startValkeyMR(ctx, t)

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	st, err := postgres.New(ctx, dsn, 16, logger)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}

	defer func() { _ = st.Close() }()

	prefix := "test:" + t.Name()
	q := valkeyqueue.New(rclient, valkeyqueue.Options{
		JobsKey:       prefix + ":jobs",
		InFlightKey:   prefix + ":in-flight",
		ReaperLockKey: prefix + ":lock:reaper",
		Logger:        logger,
	})

	const (
		jobs    = 1000
		workers = 10
	)

	for i := range jobs {
		if err := q.Enqueue(ctx, queue.Job{
			InstallationID: 1,
			Owner:          "octo",
			Repo:           fmt.Sprintf("r%04d", i),
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	subCtx, subCancel := context.WithTimeout(ctx, 60*time.Second)
	defer subCancel()

	var (
		mu    sync.Mutex
		seen  = make(map[string]int)
		count atomic.Int32
		wg    sync.WaitGroup
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			_ = q.Subscribe(subCtx, func(hctx context.Context, j queue.Job) error {
				now := time.Now().UTC()
				if err := st.UpdateRepoState(hctx, &store.RepoState{
					InstallationID:  j.InstallationID,
					Owner:           j.Owner,
					Repo:            j.Repo,
					LastCheckedAt:   &now,
					LastCheckStatus: store.StatusSuccess,
					PolicyVersion:   "v1",
				}); err != nil {
					return err
				}

				mu.Lock()
				seen[j.Repo]++
				mu.Unlock()

				if int(count.Add(1)) >= jobs {
					subCancel()
				}

				return nil
			})
		}()
	}

	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("expected %d unique jobs, got %d", jobs, len(seen))
	}

	for repo, n := range seen {
		if n != 1 {
			t.Fatalf("job %s processed %d times", repo, n)
		}
	}

	stale, err := st.StaleRepos(ctx, time.Hour, "v0", jobs+10)
	if err != nil {
		t.Fatalf("StaleRepos: %v", err)
	}

	if len(stale) != jobs {
		t.Fatalf("expected %d rows under v0 (policy_version drift), got %d", jobs, len(stale))
	}
}
