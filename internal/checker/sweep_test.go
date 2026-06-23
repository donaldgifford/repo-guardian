package checker_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// recordingQueue is a test-local queue.Queue. Records enqueued jobs
// for assertions; not a backend replacement (see DESIGN-0018).
type recordingQueue struct {
	mu   sync.Mutex
	jobs []queue.Job
}

func newRecordingQueue() *recordingQueue { return &recordingQueue{} }

func (r *recordingQueue) Enqueue(_ context.Context, j queue.Job) error { //nolint:gocritic // interface contract
	r.mu.Lock()
	r.jobs = append(r.jobs, j)
	r.mu.Unlock()

	return nil
}

func (*recordingQueue) Subscribe(ctx context.Context, _ func(context.Context, queue.Job) error) error {
	<-ctx.Done()

	return ctx.Err()
}

func (*recordingQueue) Close() error { return nil }

func (r *recordingQueue) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.jobs)
}

func (r *recordingQueue) Jobs() []queue.Job {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]queue.Job, len(r.jobs))
	copy(out, r.jobs)

	return out
}

// fakeStore is a test-local store.Store. Holds RepoStates by key and
// computes StaleRepos in-process. Not a backend replacement (see
// DESIGN-0018) — just enough functionality for the sweep tests.
type fakeStore struct {
	mu     sync.Mutex
	states map[string]*store.RepoState
}

func newFakeStore() *fakeStore {
	return &fakeStore{states: make(map[string]*store.RepoState)}
}

func fakeStoreKey(installationID int64, owner, repo string) string {
	return strconv.FormatInt(installationID, 10) + "|" + owner + "|" + repo
}

func (f *fakeStore) GetRepoState(_ context.Context, installationID int64, owner, repo string) (*store.RepoState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if s, ok := f.states[fakeStoreKey(installationID, owner, repo)]; ok {
		return s, nil
	}

	return nil, store.ErrNotFound
}

func (f *fakeStore) UpdateRepoState(_ context.Context, s *store.RepoState) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := *s
	f.states[fakeStoreKey(s.InstallationID, s.Owner, s.Repo)] = &cp

	return nil
}

func (f *fakeStore) UpsertIfMissing(_ context.Context, s *store.RepoState) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := fakeStoreKey(s.InstallationID, s.Owner, s.Repo)
	if _, ok := f.states[key]; ok {
		return false, nil
	}

	cp := *s
	if cp.LastCheckStatus == "" {
		cp.LastCheckStatus = store.StatusPending
	}

	f.states[key] = &cp

	return true, nil
}

func (f *fakeStore) StaleRepos(_ context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]store.RepoState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cutoff := time.Now().Add(-freshness)
	out := make([]store.RepoState, 0)

	for _, s := range f.states {
		stale := s.LastCheckedAt == nil || s.LastCheckedAt.Before(cutoff)
		drifted := s.PolicyVersion != currentPolicyVersion

		if stale || drifted {
			out = append(out, *s)
			if len(out) == limit {
				break
			}
		}
	}

	return out, nil
}

func (*fakeStore) Close() error { return nil }

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
	st := newFakeStore()
	q := newRecordingQueue()
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
	st := newFakeStore()
	q := newRecordingQueue()

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
	st := newFakeStore()
	q := newRecordingQueue()
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
	st := newFakeStore()
	q := newRecordingQueue()

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

	jobs := q.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 enqueued (policy version drift), got %d", len(jobs))
	}

	if jobs[0].Trigger != queue.TriggerScheduler {
		t.Fatalf("trigger: got %q want %q", jobs[0].Trigger, queue.TriggerScheduler)
	}
}
