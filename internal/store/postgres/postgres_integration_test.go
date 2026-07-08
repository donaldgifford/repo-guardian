//go:build integration

// Package postgres integration tests run against a real PostgreSQL
// instance provisioned with testcontainers-go. Build them only under
// the `integration` tag — the standard `go test ./...` path stays
// hermetic and fast.
//
//	go test -tags=integration ./internal/store/postgres/...
package postgres_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
)

// startPostgres provisions a Postgres 16 container and returns the DSN
// + a cleanup function. The container is wired with `pg_isready` wait
// strategy so the test only proceeds once the database accepts
// connections; previous attempts using port-only waits flaked at high
// host load.
func startPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()

	container, err := tcpostgres.Run(
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
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	return dsn
}

// newStore migrates the schema and constructs a Store against dsn.
// Centralised so every test exercises the same startup path.
func newStore(ctx context.Context, t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	if err := postgres.Migrate(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	s, err := postgres.New(ctx, dsn, 4, logger)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})

	return s
}

func TestPostgresStore_UpsertAndReadBack(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC().Truncate(time.Microsecond)
	rs := &store.RepoState{
		InstallationID:  42,
		Owner:           "octo",
		Repo:            "alpha",
		LastCheckedAt:   &ts,
		LastCheckStatus: store.StatusSuccess,
		LastError:       "",
		PolicyVersion:   "abc123",
	}

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	got, err := s.GetRepoState(ctx, 42, "octo", "alpha")
	if err != nil {
		t.Fatalf("get after insert: %v", err)
	}

	if got.PolicyVersion != "abc123" || got.LastCheckStatus != store.StatusSuccess {
		t.Fatalf("unexpected state after insert: %+v", got)
	}

	rs.PolicyVersion = "def456"
	rs.LastCheckStatus = store.StatusError
	rs.LastError = "boom"

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err = s.GetRepoState(ctx, 42, "octo", "alpha")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}

	if got.PolicyVersion != "def456" || got.LastError != "boom" {
		t.Fatalf("upsert did not overwrite: %+v", got)
	}
}

func TestPostgresStore_GetNotFound(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	_, err := s.GetRepoState(ctx, 1, "ghost", "repo")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPostgresStore_StaleByFreshness(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-1 * time.Minute)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "old",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "fresh",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "never",
		LastCheckedAt: nil, LastCheckStatus: store.StatusPending, PolicyVersion: "v1",
	})

	stale, err := s.StaleRepos(ctx, time.Hour, "v1", 10)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}

	if len(stale) != 2 {
		t.Fatalf("expected 2 stale rows (never, old), got %d (%+v)", len(stale), stale)
	}

	if stale[0].Repo != "never" {
		t.Fatalf("expected NULLS FIRST ordering, got %q first", stale[0].Repo)
	}
}

func TestPostgresStore_StaleByPolicyVersion(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	recent := time.Now().UTC().Add(-1 * time.Minute)

	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "matched",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v2",
	})
	mustUpdate(ctx, t, s, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "drifted",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	})

	stale, err := s.StaleRepos(ctx, time.Hour, "v2", 10)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}

	if len(stale) != 1 || stale[0].Repo != "drifted" {
		t.Fatalf("expected only 'drifted', got %+v", stale)
	}
}

func TestPostgresStore_UpsertIfMissing(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	// First call inserts a fresh row.
	created, err := s.UpsertIfMissing(ctx, &store.RepoState{
		InstallationID: 7,
		Owner:          "octo",
		Repo:           "new-repo",
		PolicyVersion:  "v1",
	})
	if err != nil {
		t.Fatalf("first UpsertIfMissing: %v", err)
	}

	if !created {
		t.Fatal("expected created=true on fresh insert")
	}

	got, err := s.GetRepoState(ctx, 7, "octo", "new-repo")
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}

	if got.LastCheckedAt != nil {
		t.Fatalf("expected nil LastCheckedAt, got %v", got.LastCheckedAt)
	}

	if got.LastCheckStatus != store.StatusPending {
		t.Fatalf("expected StatusPending, got %q", got.LastCheckStatus)
	}

	// Second call on the same key is a no-op; created=false and the
	// existing row is unchanged.
	ts := time.Now().UTC()

	rs := &store.RepoState{
		InstallationID:  7,
		Owner:           "octo",
		Repo:            "new-repo",
		LastCheckedAt:   &ts,
		LastCheckStatus: store.StatusSuccess,
		PolicyVersion:   "v2",
	}
	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("UpdateRepoState to populate state: %v", err)
	}

	created, err = s.UpsertIfMissing(ctx, &store.RepoState{
		InstallationID: 7,
		Owner:          "octo",
		Repo:           "new-repo",
		PolicyVersion:  "v3", // intentionally different — must NOT overwrite.
	})
	if err != nil {
		t.Fatalf("second UpsertIfMissing: %v", err)
	}

	if created {
		t.Fatal("expected created=false on existing row")
	}

	got, err = s.GetRepoState(ctx, 7, "octo", "new-repo")
	if err != nil {
		t.Fatalf("get after second upsert: %v", err)
	}

	if got.PolicyVersion != "v2" {
		t.Fatalf("UpsertIfMissing overwrote PolicyVersion: %q (wanted v2)", got.PolicyVersion)
	}

	if got.LastCheckStatus != store.StatusSuccess {
		t.Fatalf("UpsertIfMissing overwrote LastCheckStatus: %q (wanted success)", got.LastCheckStatus)
	}
}

// TestPostgresStore_UpsertIfMissing_ConcurrentRace exercises the
// IMPL-0015 Testing Plan race-condition checkbox: simultaneous
// webhook + Discoverer writes for the same repo. The contract is
// that ON CONFLICT DO NOTHING returns the *atomic* "did I insert this
// row" flag — exactly one goroutine across N parallel callers must
// see created=true, all others created=false.
func TestPostgresStore_UpsertIfMissing_ConcurrentRace(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)
	s := newStore(ctx, t, dsn)

	const workers = 16

	var (
		wg       sync.WaitGroup
		startGun = make(chan struct{})
		created  atomic.Int32
		errCount atomic.Int32
	)

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-startGun

			ok, err := s.UpsertIfMissing(ctx, &store.RepoState{
				InstallationID: 99,
				Owner:          "race",
				Repo:           "contended",
				PolicyVersion:  "v1",
			})
			if err != nil {
				errCount.Add(1)
				return
			}

			if ok {
				created.Add(1)
			}
		}()
	}

	close(startGun)
	wg.Wait()

	if got := errCount.Load(); got != 0 {
		t.Fatalf("UpsertIfMissing errored %d/%d times under contention", got, workers)
	}

	if got := created.Load(); got != 1 {
		t.Fatalf("expected exactly 1 created=true under contention, got %d/%d", got, workers)
	}
}

func TestPostgresStore_MigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(ctx, t)

	for i := range 3 {
		if err := postgres.Migrate(dsn); err != nil {
			t.Fatalf("migrate apply %d: %v", i, err)
		}
	}

	s := newStore(ctx, t, dsn)

	ts := time.Now().UTC()
	if err := s.UpdateRepoState(ctx, &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "after-migrate",
		LastCheckedAt: &ts, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("upsert after repeated migrate: %v", err)
	}
}

func mustUpdate(ctx context.Context, t *testing.T, s *postgres.Store, rs *store.RepoState) {
	t.Helper()

	if err := s.UpdateRepoState(ctx, rs); err != nil {
		t.Fatalf("UpdateRepoState %s/%s: %v", rs.Owner, rs.Repo, err)
	}
}
