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

	"github.com/donaldgifford/repo-guardian/internal/budget"
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

// EnqueueAfter records like Enqueue, stamping the due-time on the
// recorded job so tests can assert deferral scheduling.
func (r *recordingQueue) EnqueueAfter(ctx context.Context, j queue.Job, at time.Time) error { //nolint:gocritic // interface contract
	j.AvailableAt = at

	return r.Enqueue(ctx, j)
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

func (f *fakeRateLimit) RateLimitRemaining(_ context.Context, id int64) (int, int, time.Time, error) {
	if f.err != nil {
		return 0, 0, time.Time{}, f.err
	}

	rem, ok := f.remaining[id]
	if !ok {
		return f.limit, f.limit, time.Time{}, nil
	}

	return rem, f.limit, time.Time{}, nil
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

func TestStaleSweeper_BudgetTracker_GatesEnqueueWhenSpendableZero(t *testing.T) {
	st := newFakeStore()
	q := newRecordingQueue()
	rl := &fakeRateLimit{remaining: map[int64]int{1: 5000}, limit: 5000}

	metrics.EnqueueGatedByBudgetTotal.Reset()

	// Tracker with remaining=100 < reserve floor (1000) → spendable=0.
	tracker := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})
	rlClient := &budgetTrackerFakeClient{remaining: 100, limit: 5000, resetAt: time.Now().Add(time.Hour)}

	if err := tracker.RefreshFromAPI(t.Context(), rlClient, 1); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	old := time.Now().Add(-2 * time.Hour)
	if err := st.UpdateRepoState(t.Context(), &store.RepoState{
		InstallationID: 1, Owner: "o", Repo: "r1",
		LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Budget: tracker, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 0 {
		t.Fatalf("expected 0 enqueued (budget-gated), got %d", got)
	}

	if got := testutil.ToFloat64(metrics.EnqueueGatedByBudgetTotal.WithLabelValues(strconv.Itoa(1))); got != 1 {
		t.Fatalf("expected enqueue_gated_by_budget_total=1, got %v", got)
	}
}

func TestStaleSweeper_BudgetTracker_DecrementsOnSuccessfulEnqueue(t *testing.T) {
	st := newFakeStore()
	q := newRecordingQueue()
	rl := &fakeRateLimit{remaining: map[int64]int{1: 5000}, limit: 5000}

	tracker := budget.New(budget.Options{ReserveFraction: 0.20, CostPerRepo: 10})
	rlClient := &budgetTrackerFakeClient{remaining: 2000, limit: 5000, resetAt: time.Now().Add(time.Hour)}

	if err := tracker.RefreshFromAPI(t.Context(), rlClient, 1); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	preSpendable, _ := tracker.SpendableForEnqueue(1)
	if preSpendable != 100 {
		t.Fatalf("pre-sweep spendable = %d, want 100", preSpendable)
	}

	// Seed 3 stale repos for installation 1.
	old := time.Now().Add(-2 * time.Hour)
	for _, name := range []string{"r1", "r2", "r3"} {
		if err := st.UpdateRepoState(t.Context(), &store.RepoState{
			InstallationID: 1, Owner: "o", Repo: name,
			LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Budget: tracker, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10, Reserve: 0.1,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 3 {
		t.Fatalf("expected 3 enqueued, got %d", got)
	}

	// 3 decrements of 10 = 30. remaining was 2000 → now 1970.
	// usable = 1970 - 1000 = 970; spendable = 97.
	postSpendable, _ := tracker.SpendableForEnqueue(1)
	if postSpendable != 97 {
		t.Errorf("post-sweep spendable = %d, want 97 (3 enqueues × 10 cost)", postSpendable)
	}
}

// budgetTrackerFakeClient is a budget.RateLimitClient stub for the
// sweep_test budget-gating tests. Distinct from fakeRateLimit (which
// implements checker.RateLimitProvider with the same signature but
// in a different package context).
type budgetTrackerFakeClient struct {
	remaining int
	limit     int
	resetAt   time.Time
}

func (f *budgetTrackerFakeClient) RateLimitRemaining(_ context.Context, _ int64) (int, int, time.Time, error) {
	return f.remaining, f.limit, f.resetAt, nil
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
