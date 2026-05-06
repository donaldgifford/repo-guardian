package checker_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	memqueue "github.com/donaldgifford/repo-guardian/internal/queue/memory"
	"github.com/donaldgifford/repo-guardian/internal/store"
	memstore "github.com/donaldgifford/repo-guardian/internal/store/memory"
)

type fakeRateLimit struct {
	remaining map[int64]int
	limit     int
	err       error
}

func (f *fakeRateLimit) RateLimitRemaining(_ context.Context, id int64) (int, int, error) {
	if f.err != nil {
		return 0, 0, f.err
	}

	rem, ok := f.remaining[id]
	if !ok {
		return f.limit, f.limit, nil
	}

	return rem, f.limit, nil
}

func warnLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestStaleSweeper_EnqueuesAllWhenBudgetIsAmple(t *testing.T) {
	st := memstore.New()
	q := memqueue.New(8)
	rl := &fakeRateLimit{remaining: map[int64]int{}, limit: 5000}

	old := time.Now().Add(-2 * time.Hour)
	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r1",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 2, Owner: "p", Repo: "r2",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got, want := q.Len(), 2; got != want {
		t.Fatalf("queue len: got %d want %d", got, want)
	}
}

func TestStaleSweeper_SkipsInstallationOverReserve(t *testing.T) {
	st := memstore.New()
	q := memqueue.New(8)

	// remaining=499, limit=5000, reserve=0.1 → threshold=500.
	// remaining (499) ≤ threshold (500) → gated.
	rl := &fakeRateLimit{remaining: map[int64]int{1: 499}, limit: 5000}

	metrics.RateLimitReserveBlockedTotal.Reset()

	old := time.Now().Add(-2 * time.Hour)
	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r1",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 0 {
		t.Fatalf("expected 0 enqueued (rate-limited), got %d", got)
	}

	if got := testutil.ToFloat64(metrics.RateLimitReserveBlockedTotal.WithLabelValues(strconv.Itoa(1))); got != 1 {
		t.Fatalf("expected blocked counter=1, got %v", got)
	}
}

func TestStaleSweeper_RateLimitErrorFallsOpen(t *testing.T) {
	st := memstore.New()
	q := memqueue.New(8)
	rl := &fakeRateLimit{err: errors.New("transient")}

	old := time.Now().Add(-2 * time.Hour)
	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r1",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 1 {
		t.Fatalf("expected 1 enqueued (rate-limit lookup falls open), got %d", got)
	}
}

func TestStaleSweeper_PolicyVersionMismatchEnqueuesAll(t *testing.T) {
	st := memstore.New()
	q := memqueue.New(8)

	recent := time.Now().Add(-1 * time.Minute)
	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "fresh-but-old-policy",
		LastCheckedAt: &recent, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v2", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 1 {
		t.Fatalf("expected 1 enqueued (policy version drift), got %d", got)
	}

	// Drain to inspect the job.
	job := <-receiveOne(t, q)
	if job.Trigger != queue.TriggerScheduler {
		t.Fatalf("trigger: got %q want %q", job.Trigger, queue.TriggerScheduler)
	}
}

// receiveOne pulls one job off q and returns it. Used to inspect job
// contents without holding a Subscribe goroutine for the test
// lifecycle.
func receiveOne(t *testing.T, q queue.Queue) <-chan queue.Job {
	t.Helper()

	ch := make(chan queue.Job, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)

	go func() {
		defer cancel()
		_ = q.Subscribe(ctx, func(_ context.Context, j queue.Job) error {
			ch <- j
			cancel()

			return nil
		})
	}()

	return ch
}
