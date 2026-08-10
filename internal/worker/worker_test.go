package worker_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/github/mocks"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/queue"
	"github.com/donaldgifford/repo-guardian/internal/reconciler"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/worker"
)

// recordingQueue is a test-local queue.Queue stub. Subscribe blocks
// until the context is cancelled (mirroring how a real backend
// behaves when idle). Not a backend replacement — see DESIGN-0018.
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

// TestPool_StartStop verifies that the pool launches and shuts down
// cleanly with no jobs delivered.
func TestPool_StartStop(t *testing.T) {
	t.Parallel()

	q := newRecordingQueue()

	defer func() { _ = q.Close() }()

	p := worker.New(q, nil, nil, nil, "", 10, 2, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	cancel()
	p.Stop()
}

// TestPool_StopIdempotent verifies that calling Stop multiple times
// is safe.
func TestPool_StopIdempotent(t *testing.T) {
	t.Parallel()

	q := newRecordingQueue()
	p := worker.New(q, nil, nil, nil, "", 10, 1, slog.Default())

	p.Stop() // before Start
	p.Stop() // double-Stop, also fine
}

// deliverOnceQueue hands the Subscribe handler exactly one crafted
// job and captures its return value — the minimal per-file recorder
// for exercising processJob outcomes (DESIGN-0018 convention).
type deliverOnceQueue struct {
	job    queue.Job
	result chan error
}

func (*deliverOnceQueue) Enqueue(context.Context, queue.Job) error { return nil }

func (*deliverOnceQueue) EnqueueAfter(context.Context, queue.Job, time.Time) error { return nil }

func (d *deliverOnceQueue) Subscribe(ctx context.Context, h func(context.Context, queue.Job) error) error {
	d.result <- h(ctx, d.job)

	return nil
}

func (*deliverOnceQueue) Close() error { return nil }

var errUnimplemented = errors.New("not implemented in capturingStore")

// capturingStore records UpdateRepoState calls; every other Store
// method is a no-op.
type capturingStore struct {
	mu          sync.Mutex
	states      []store.RepoState
	deactivated []string
}

func (*capturingStore) GetRepoState(context.Context, int64, string, string) (*store.RepoState, error) {
	return nil, errUnimplemented
}

func (c *capturingStore) UpdateRepoState(_ context.Context, s *store.RepoState) error {
	c.mu.Lock()
	c.states = append(c.states, *s)
	c.mu.Unlock()

	return nil
}

func (*capturingStore) UpsertIfMissing(context.Context, *store.RepoState) (bool, error) {
	return false, nil
}

func (*capturingStore) StaleRepos(context.Context, time.Duration, string, int) ([]store.RepoState, error) {
	return nil, nil
}

func (*capturingStore) Close() error { return nil }

// TestPool_AttemptCap_TerminalDisposition locks IMPL-0022 task 4.4:
// a job delivered at the MAX_JOB_ATTEMPTS cap is dropped with the
// terminal disposition — exactly one StatusError repo_state write
// with a descriptive LastError, an exhausted-counter increment, and
// a nil handler return so the queue acks (drops) rather than
// retrying. The nil engine/ghClient prove the job is refused before
// any processing is attempted.
func TestPool_AttemptCap_TerminalDisposition(t *testing.T) {
	// Not parallel: reads a package-global metric after Reset.
	metrics.QueueAttemptsExhaustedTotal.Reset()

	q := &deliverOnceQueue{
		job: queue.Job{
			ID:             "cap",
			InstallationID: 7,
			Owner:          "o",
			Repo:           "r",
			Trigger:        queue.TriggerScheduler,
			Attempts:       10,
		},
		result: make(chan error, 1),
	}
	st := &capturingStore{}
	p := worker.New(q, nil, nil, st, "pv1", 10, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	var res error
	select {
	case res = <-q.result:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	if res != nil {
		t.Errorf("handler returned %v, want nil (ack-and-drop terminal disposition)", res)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.states) != 1 {
		t.Fatalf("UpdateRepoState calls = %d, want exactly 1 (terminal disposition happens once)", len(st.states))
	}

	got := st.states[0]
	if got.LastCheckStatus != store.StatusError {
		t.Errorf("LastCheckStatus = %q, want %q", got.LastCheckStatus, store.StatusError)
	}

	if !strings.Contains(got.LastError, "MAX_JOB_ATTEMPTS") {
		t.Errorf("LastError = %q, want it to name MAX_JOB_ATTEMPTS", got.LastError)
	}

	if got.InstallationID != 7 || got.Owner != "o" || got.Repo != "r" {
		t.Errorf("repo_state row = %+v, want installation 7 o/r", got)
	}

	if v := testutil.ToFloat64(metrics.QueueAttemptsExhaustedTotal.WithLabelValues("7")); v != 1 {
		t.Errorf("queue_attempts_exhausted_total{installation_id=7} = %v, want 1", v)
	}
}

// Pre-IMPL-0016 the worker test suite included two further tests
// (TestPool_DrainsQueue, TestPool_HandlerErrorContinues) that
// exercised memqueue's Subscribe pump rather than the worker pool
// itself — they spawned a goroutine calling q.Subscribe directly,
// never invoking worker.New. With memqueue removed, the equivalent
// pump-correctness behaviour is covered by the queue/valkey
// integration tests (EnqueueDequeue + CloseUnblocksSubscribe under
// the integration build tag).

// Deactivate records the park so the access-denied test can assert the
// repo was taken out of the sweep, not merely that the job was acked.
func (c *capturingStore) Deactivate(_ context.Context, installationID int64, owner, repo string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deactivated = append(c.deactivated, fmt.Sprintf("%d/%s/%s", installationID, owner, repo))

	return nil
}

// notFoundClient is a ghclient.Client whose GetRepository fails the way
// GitHub answers a repository the installation cannot see: 404, not 403.
// Everything else panics via the embedded generated mock, which is the
// point — this test must fail loudly if the engine starts reaching past
// the repository probe.
type notFoundClient struct {
	mocks.MockClient
}

func (c *notFoundClient) CreateInstallationClient(context.Context, int64) (ghclient.Client, error) {
	return c, nil
}

func (*notFoundClient) GetRepository(context.Context, string, string) (*ghclient.Repository, error) {
	return nil, &gh.ErrorResponse{
		Response: &http.Response{StatusCode: http.StatusNotFound},
		Message:  "Not Found",
	}
}

// TestPool_AccessDenied_ParksRepoWithoutRetrying pins the INV-0015
// circuit breaker.
//
// Before it, a repository the App could not read took the generic error
// path: nack, requeue, Attempts++, up to MAX_JOB_ATTEMPTS — and the next
// stale sweep handed it straight back, so it burned the whole attempt
// budget every cycle, forever, while its failures were indistinguishable
// from a transient 500 in both logs and metrics.
//
// Three things must hold together: the handler acks (returns nil) so the
// job is dropped rather than retried, the row is deactivated so the sweep
// stops re-enqueuing it, and the failure lands on its own metric series.
func TestPool_AccessDenied_ParksRepoWithoutRetrying(t *testing.T) {
	// Not parallel: reads a package-global metric after Reset.
	metrics.ReposParkedTotal.Reset()

	// No file rules: CheckRepo fails at the GetRepository probe before it
	// evaluates any, so rules would only add reconciler wiring this test
	// does not exercise.
	cfg := &policy.PolicyConfig{Guardian: policy.BuiltinDefaults().Guardian}

	eng, err := checker.NewEngine(cfg, rules.NewTemplateStore(), slog.Default(), reconciler.NewRegistry())
	if err != nil {
		t.Fatalf("NewEngine() = _, %v, want nil error", err)
	}

	q := &deliverOnceQueue{
		job: queue.Job{
			ID:             "denied",
			InstallationID: 9,
			Owner:          "acme",
			Repo:           "secret",
			Trigger:        queue.TriggerScheduler,
		},
		result: make(chan error, 1),
	}
	st := &capturingStore{}
	p := worker.New(q, eng, &notFoundClient{}, st, "pv1", 10, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	var res error
	select {
	case res = <-q.result:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	// 1. Acked. Returning an error here would rebuild the retry loop.
	if res != nil {
		t.Errorf("handler returned %v, want nil — an error re-nacks and the job retries against a repo that can never succeed", res)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// 2. Parked, so the sweep stops handing it back.
	want := "9/acme/secret"
	if len(st.deactivated) != 1 || st.deactivated[0] != want {
		t.Errorf("Deactivate calls = %v, want exactly [%s]", st.deactivated, want)
	}

	if len(st.states) != 1 {
		t.Fatalf("UpdateRepoState calls = %d, want exactly 1", len(st.states))
	}

	if got := st.states[0]; got.LastCheckStatus != store.StatusError {
		t.Errorf("LastCheckStatus = %q, want %q", got.LastCheckStatus, store.StatusError)
	}

	// 3. Its own series, so an operator can alert on "the App lost access"
	// without it being buried among transient 500s.
	if v := testutil.ToFloat64(metrics.ReposParkedTotal.WithLabelValues("acme", "9", "access_denied")); v != 1 {
		t.Errorf("repos_parked_total{org=acme, installation_id=9, reason=access_denied} = %v, want 1", v)
	}

	if v := testutil.ToFloat64(metrics.ErrorsTotal.WithLabelValues("check_repo", "acme")); v != 0 {
		t.Errorf("errors_total{operation=check_repo} = %v, want 0 — access denial must not land in the generic bucket", v)
	}
}

// repoStateClient reports a repository that exists and is readable, in
// whatever lifecycle state the test needs. Every skip reason the engine can
// return is a property of this struct, so the parked and not-parked cases are
// the same fake with one field flipped.
type repoStateClient struct {
	mocks.MockClient

	repo ghclient.Repository
}

func (c *repoStateClient) CreateInstallationClient(context.Context, int64) (ghclient.Client, error) {
	return c, nil
}

func (c *repoStateClient) GetRepository(_ context.Context, owner, repo string) (*ghclient.Repository, error) {
	r := c.repo
	r.Owner, r.Name = owner, repo

	return &r, nil
}

// TestPool_ArchivedRepo_IsParkedNotRechecked pins the second half of the
// INV-0015 parking work.
//
// An archived repository was skipped correctly but never stopped costing
// anything: enqueue, installation client, GetRepository, skip, write back
// success, and round again every freshness cycle for as long as the repo
// exists. Discovery filters archived repos, so parking is stable — and it
// self-heals, because an unarchived repo stops being filtered and
// UpsertIfMissing reactivates it.
//
// StatusSkipped, not StatusError: nothing went wrong.
func TestPool_ArchivedRepo_IsParkedNotRechecked(t *testing.T) {
	// Not parallel: reads a package-global metric after Reset.
	metrics.ReposParkedTotal.Reset()

	cfg := &policy.PolicyConfig{Guardian: policy.BuiltinDefaults().Guardian}

	eng, err := checker.NewEngine(cfg, rules.NewTemplateStore(), slog.Default(), reconciler.NewRegistry())
	if err != nil {
		t.Fatalf("NewEngine() = _, %v, want nil error", err)
	}

	q := &deliverOnceQueue{
		job: queue.Job{
			ID: "arch", InstallationID: 4, Owner: "acme", Repo: "old",
			Trigger: queue.TriggerScheduler,
		},
		result: make(chan error, 1),
	}
	st := &capturingStore{}
	client := &repoStateClient{repo: ghclient.Repository{Archived: true, HasBranch: true, DefaultRef: "main"}}
	p := worker.New(q, eng, client, st, "pv1", 10, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	var res error
	select {
	case res = <-q.result:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	if res != nil {
		t.Errorf("handler returned %v, want nil — a skip is not a failure", res)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	want := "4/acme/old"
	if len(st.deactivated) != 1 || st.deactivated[0] != want {
		t.Errorf("Deactivate calls = %v, want exactly [%s]; an archived repo must leave the sweep", st.deactivated, want)
	}

	if len(st.states) != 1 {
		t.Fatalf("UpdateRepoState calls = %d, want exactly 1", len(st.states))
	}

	if got := st.states[0].LastCheckStatus; got != store.StatusSkipped {
		t.Errorf("LastCheckStatus = %q, want %q — nothing failed, and no rules were evaluated either",
			got, store.StatusSkipped)
	}

	// There is no park_reason column; last_error is the only free-text slot,
	// and it is what makes `SELECT ... WHERE NOT active` answer *why* a row
	// is parked. The documented operator query in docs/operations/migrations.md
	// depends on this.
	if got := st.states[0].LastError; got != checker.SkipArchived {
		t.Errorf("LastError = %q, want %q — the park reason must be recoverable from SQL",
			got, checker.SkipArchived)
	}

	if v := testutil.ToFloat64(metrics.ReposParkedTotal.WithLabelValues("acme", "4", "archived")); v != 1 {
		t.Errorf("repos_parked_total{reason=archived} = %v, want 1", v)
	}

	if v := testutil.ToFloat64(metrics.ErrorsTotal.WithLabelValues("check_repo", "acme")); v != 0 {
		t.Errorf("errors_total{operation=check_repo} = %v, want 0 — an archived repo is not an error", v)
	}
}

// TestPool_EmptyRepo_IsSkippedButNotParked is the boundary of the parking
// change, and the reason skipReason returns a durable flag rather than just a
// reason string.
//
// Parking is only stable when the engine's skip conditions are a SUBSET of
// discovery's filters. Discovery filters archived and fork repos, so a parked
// one stays parked. Discovery does NOT filter empty repos — it would re-upsert
// this repo on its very next pass, flipping active back to true, and the pair
// would churn against each other at the discovery interval forever.
//
// So an empty repo is skipped the old way: no park, no error, and a normal
// success write-back that lets freshness govern the next check. An empty repo
// also stops being empty on its first push, which is a webhook away.
func TestPool_EmptyRepo_IsSkippedButNotParked(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{Guardian: policy.BuiltinDefaults().Guardian}

	eng, err := checker.NewEngine(cfg, rules.NewTemplateStore(), slog.Default(), reconciler.NewRegistry())
	if err != nil {
		t.Fatalf("NewEngine() = _, %v, want nil error", err)
	}

	q := &deliverOnceQueue{
		job: queue.Job{
			ID: "empty", InstallationID: 4, Owner: "acme", Repo: "fresh",
			Trigger: queue.TriggerScheduler,
		},
		result: make(chan error, 1),
	}
	st := &capturingStore{}
	client := &repoStateClient{repo: ghclient.Repository{HasBranch: false}}
	p := worker.New(q, eng, client, st, "pv1", 10, 1, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	select {
	case err := <-q.result:
		if err != nil {
			t.Errorf("handler returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.deactivated) != 0 {
		t.Errorf("Deactivate calls = %v, want none; discovery does not filter empty repos, "+
			"so parking one makes discovery and the sweep fight over it every interval", st.deactivated)
	}

	if len(st.states) != 1 {
		t.Fatalf("UpdateRepoState calls = %d, want exactly 1", len(st.states))
	}

	if got := st.states[0].LastCheckStatus; got != store.StatusSuccess {
		t.Errorf("LastCheckStatus = %q, want %q", got, store.StatusSuccess)
	}
}
