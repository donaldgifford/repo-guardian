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

	"github.com/donaldgifford/repo-guardian/internal/checker"
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

// The stale sweeper reads and enqueues; rule states are the worker's
// write-back, never the sweeper's.
func (*fakeStore) UpsertRuleStates(
	_ context.Context, _ int64, _, _ string, _ []store.RuleState,
) error {
	return nil
}

func (*fakeStore) Close() error { return nil }

type fakeRateLimit struct {
	remaining map[int64]int
	limit     int
	err       error
	calls     map[int64]int
}

func (f *fakeRateLimit) RateLimitRemaining(_ context.Context, id int64) (int, int, time.Time, error) {
	if f.calls == nil {
		f.calls = make(map[int64]int)
	}

	f.calls[id]++

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

func TestStaleSweeper_EnqueuesAllStale(t *testing.T) {
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
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got, want := q.Len(), 2; got != want {
		t.Fatalf("queue len: got %d want %d", got, want)
	}
}

// TestStaleSweeper_SamplesRateLimitOncePerInstallation locks the
// DESIGN-0021 OQ4 rider: the reserve gate is gone, but the sweep must
// still call RateLimitRemaining exactly once per installation per
// sweep — that call is the only producer feeding
// rate_limit_remaining{installation_id}, and it must never gate.
func TestStaleSweeper_SamplesRateLimitOncePerInstallation(t *testing.T) {
	st := newFakeStore()
	q := newRecordingQueue()

	// remaining=0: pre-IMPL-0022 the reserve gate would have skipped
	// everything; now every repo still enqueues.
	rl := &fakeRateLimit{remaining: map[int64]int{1: 0, 2: 0}, limit: 5000}

	old := time.Now().Add(-2 * time.Hour)
	for _, seed := range []struct {
		install int64
		repo    string
	}{{1, "r1"}, {1, "r2"}, {2, "r3"}} {
		if err := st.UpdateRepoState(t.Context(), &store.RepoState{
			InstallationID: seed.install, Owner: "o", Repo: seed.repo,
			LastCheckedAt: &old, LastCheckStatus: store.StatusSuccess, PolicyVersion: "v1",
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.repo, err)
		}
	}

	sw := checker.NewStaleSweeper(checker.StaleSweeperOptions{
		Store: st, Queue: q, RateLimit: rl, Logger: warnLogger(),
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 3 {
		t.Fatalf("queue len = %d, want 3 (exhausted budget must not gate enqueue)", got)
	}

	for _, install := range []int64{1, 2} {
		if got := rl.calls[install]; got != 1 {
			t.Errorf("RateLimitRemaining calls for installation %d = %d, want 1", install, got)
		}
	}
}

func TestStaleSweeper_RateLimitSampleErrorDoesNotBlock(t *testing.T) {
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
		Freshness: time.Hour, PolicyVersion: "v1", BatchSize: 10,
	})

	if err := sw.SweepStale(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got := q.Len(); got != 1 {
		t.Fatalf("expected 1 enqueued (sampling is observability-only), got %d", got)
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
		Freshness: time.Hour, PolicyVersion: "v2", BatchSize: 10,
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

func (*fakeStore) Deactivate(context.Context, int64, string, string) error { return nil }

func (*fakeStore) Posture(context.Context) (*store.Posture, error) {
	return nil, errors.New("fakeStore: Posture not used by these tests")
}
