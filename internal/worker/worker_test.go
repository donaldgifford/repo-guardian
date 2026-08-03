package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/mock"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	ghmocks "github.com/donaldgifford/repo-guardian/internal/github/mocks"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/queue"
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

// capturingStore records UpdateRepoState and UpsertRuleStates calls;
// every other Store method is a no-op.
//
// ruleWrites keeps every call, including the ones with an empty slice:
// "the worker called us with nothing" and "the worker never called us"
// are different outcomes for a repo whose rules all became
// inapplicable, and a recorder that dropped empty calls could not tell
// the two apart.
type capturingStore struct {
	mu         sync.Mutex
	states     []store.RepoState
	ruleWrites []ruleWrite
	ruleErr    error
}

// ruleWrite is one UpsertRuleStates invocation.
type ruleWrite struct {
	InstallationID int64
	Owner          string
	Repo           string
	States         []store.RuleState
}

func (c *capturingStore) UpsertRuleStates(
	_ context.Context,
	installationID int64,
	owner, repo string,
	states []store.RuleState,
) error {
	c.mu.Lock()
	c.ruleWrites = append(c.ruleWrites, ruleWrite{
		InstallationID: installationID,
		Owner:          owner,
		Repo:           repo,
		States:         slices.Clone(states),
	})
	c.mu.Unlock()

	return c.ruleErr
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

// engineForRepo builds a real policy engine with a single file rule, so
// processJob exercises the genuine CheckRepo → writeBack path rather
// than a stub.
func engineForRepo(t *testing.T) *checker.Engine {
	t.Helper()

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("template store load: %v", err)
	}

	cfg := &policy.PolicyConfig{
		Guardian:  policy.BuiltinDefaults().Guardian,
		FileRules: []policy.FileRuleConfig{{Name: "codeowners", Type: "file", Paths: []string{"CODEOWNERS"}, Template: "codeowners.tmpl"}},
	}
	cfg.Guardian.DryRun = true // stay off the PR-creation path

	e, err := checker.NewEngine(cfg, ts, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("checker.NewEngine: %v", err)
	}

	return e
}

// TestProcessJob_RuleStateFailureDoesNotFailJob closes IMPL-0023 task
// 1.6 end to end. TestWriteBack_RuleStateFailureIsBestEffort proves
// writeBack swallows the error; this proves the job outcome above it is
// unaffected, which is the property that actually matters to the queue.
//
// If someone later gives writeBack an error return and wires it into
// processJob's, a posture write failing would nack the job — and since
// the posture write happens on the SUCCESS path, a Postgres hiccup
// would turn completed work into an infinite retry loop that burns
// GitHub rate limit re-doing checks that already succeeded.
func TestProcessJob_RuleStateFailureDoesNotFailJob(t *testing.T) {
	installClient := &ghmocks.MockClient{}
	installClient.On("GetRepository", mock.Anything, "octo", "alpha").
		Return(&ghclient.Repository{Owner: "octo", Name: "alpha", HasBranch: true, DefaultRef: "main"}, nil)
	installClient.On("ListOpenPullRequests", mock.Anything, "octo", "alpha").
		Return([]*ghclient.PullRequest(nil), nil)
	installClient.On("GetContents", mock.Anything, "octo", "alpha", "CODEOWNERS").
		Return(true, nil)

	rootClient := &ghmocks.MockClient{}
	rootClient.On("CreateInstallationClient", mock.Anything, int64(42)).
		Return(ghclient.Client(installClient), nil)

	q := &deliverOnceQueue{
		job: queue.Job{
			ID: "j1", InstallationID: 42, Owner: "octo", Repo: "alpha",
			Trigger: queue.TriggerScheduler,
		},
		result: make(chan error, 1),
	}

	st := &capturingStore{ruleErr: errors.New("deadlock detected")}
	p := worker.New(q, engineForRepo(t), rootClient, st, "pv1", 10, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))

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
		t.Errorf("processJob returned %v, want nil — a posture write failure must not nack a job whose check succeeded", res)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// The failing write must still have been attempted, or this test
	// would pass for the wrong reason: a worker that silently stopped
	// writing posture altogether also never fails a job.
	if len(st.ruleWrites) != 1 {
		t.Fatalf("UpsertRuleStates calls = %d, want 1 (the attempt that failed)", len(st.ruleWrites))
	}

	if len(st.states) != 1 || st.states[0].LastCheckStatus != store.StatusSuccess {
		t.Errorf("repo_state writes = %+v, want one StatusSuccess row despite the posture failure", st.states)
	}
}

// TestProcessJob_SetsInstallationInfoEvenWhenClientFails pins the
// worker half of the org↔installation join (IMPL-0023 task 2.1).
//
// The failing-client case is the one under test on purpose. That is the
// state an operator debugs — a dead or unauthorized installation
// spiking errors_total{operation="create_install_client"} — and those
// panels are keyed by installation_id, so the org label has to exist
// precisely when the installation does not work. Emitting after a
// successful client construction would blank the label exactly then.
func TestProcessJob_SetsInstallationInfoEvenWhenClientFails(t *testing.T) {
	// Cannot use t.Parallel() — resets the global InstallationInfo gauge.
	metrics.InstallationInfo.Reset()

	rootClient := &ghmocks.MockClient{}
	rootClient.On("CreateInstallationClient", mock.Anything, int64(42)).
		Return(ghclient.Client(nil), errors.New("installation suspended"))

	q := &deliverOnceQueue{
		job: queue.Job{
			ID: "j1", InstallationID: 42, Owner: "octo", Repo: "alpha",
			Trigger: queue.TriggerScheduler,
		},
		result: make(chan error, 1),
	}

	p := worker.New(q, engineForRepo(t), rootClient, &capturingStore{}, "pv1", 10, 1,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	select {
	case res := <-q.result:
		if res == nil {
			t.Fatal("processJob returned nil, want the client-construction error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	p.Stop()

	if got := testutil.ToFloat64(metrics.InstallationInfo.WithLabelValues("42", "octo")); got != 1 {
		t.Errorf(`installation_info{installation_id="42", org="octo"} = %v, want 1`, got)
	}
}
