//go:build integration

package activities_test

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/temporalproto"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/checker"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const installationID int64 = 7

// updateHistories rewrites the replay suite's captured histories
// (internal/workflows/testdata/histories) from this run.
var updateHistories = flag.Bool("update-histories", false, "capture RepoWorkflow histories for the replay suite")

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// harness is one Postgres, one Temporal dev server and one httptest
// GitHub serving acme/widgets with no files.
type harness struct {
	pool     *pgxpool.Pool
	store    *postgres.V2Store
	temporal *temporaltest.Server
	github   *ghclient.GitHubClient
	repoID   int64
	engine   *checker.Engine
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	ctx := t.Context()
	dsn := pgtest.AppRole(t, pgtest.Start(t))

	db, err := postgres.OpenDB(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	migrator, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}

	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}

	t.Cleanup(pool.Close)

	st := postgres.NewV2Store(pool, quiet)
	if err := st.UpsertInstallation(ctx, store.Installation{InstallationID: installationID, AccountLogin: "acme"}); err != nil {
		t.Fatalf("installation: %v", err)
	}

	up, err := st.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: "acme", Name: "widgets", InstallationID: installationID})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}

	gh := httptest.NewServer(fakeGitHub(t))
	t.Cleanup(gh.Close)

	client, err := ghclient.NewClientForBaseURL(gh.URL, gh.Client().Transport, quiet, 0.10)
	if err != nil {
		t.Fatalf("github client: %v", err)
	}

	cfg := policy.BuiltinDefaults()
	cfg.Guardian.DryRun = true

	ts := rules.NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("templates: %v", err)
	}

	engine, err := checker.NewEngine(cfg, ts, quiet, nil)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	return &harness{pool: pool, store: st, temporal: temporaltest.Start(t), github: client, repoID: up.ID, engine: engine}
}

// fakeGitHub serves a repository with no files and no PRs. The policy
// is dry-run, so any write fails the test.
func fakeGitHub(t *testing.T) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":9001,"name":"widgets","full_name":"acme/widgets","owner":{"login":"acme"},"default_branch":"main"}`)
	})
	mux.HandleFunc("GET /api/v3/repos/acme/widgets/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write in dry run: %s %s", r.Method, r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	return mux
}

// factory hands out the base-URL client for every installation.
type factory struct{ c *ghclient.GitHubClient }

func (f factory) CreateInstallationClient(context.Context, int64) (ghclient.Client, error) {
	return f.c, nil
}

// startWorker runs a versioned worker with eng and makes its build the
// deployment's current version.
func (h *harness) startWorker(t *testing.T, eng activities.Engine, buildID string) worker.Worker {
	t.Helper()

	wc := temporal.WorkerConfig{TaskQueue: h.temporal.Config.TaskQueue, ActivityConcurrency: 2, BuildID: buildID}
	w := temporal.NewWorker(h.temporal.Client, &wc)
	workflows.Register(w)
	activities.New(eng, h.store, factory{h.github}, "v2:test", quiet).Register(w)
	activities.NewBudget(h.temporal.Client, wc.TaskQueue, 0.10).Register(w)

	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	handle := h.temporal.Client.WorkerDeploymentClient().GetHandle(temporal.DeploymentName)
	eventually(t, 30*time.Second, func() bool {
		_, err := handle.SetCurrentVersion(t.Context(), client.WorkerDeploymentSetCurrentVersionOptions{BuildID: buildID})

		return err == nil
	})

	return w
}

func (h *harness) startRepoWorkflow(t *testing.T) client.WorkflowRun {
	t.Helper()

	run, err := h.temporal.Client.ExecuteWorkflow(t.Context(), client.StartWorkflowOptions{
		ID:        workflows.RepoWorkflowID(h.repoID),
		TaskQueue: h.temporal.Config.TaskQueue,
	}, workflows.RepoWorkflowName, &workflows.RepoWorkflowInput{
		RepositoryID: h.repoID, InstallationID: installationID, CheckInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("start RepoWorkflow: %v", err)
	}

	return run
}

// finalChecks returns the final (non-pending) checks rows by key.
func (h *harness) finalChecks(t *testing.T) map[string]string {
	t.Helper()

	rows, err := h.pool.Query(t.Context(), `SELECT check_key, outcome FROM checks WHERE repository_id = $1 AND outcome <> 'pending'`, h.repoID)
	if err != nil {
		t.Fatalf("read checks: %v", err)
	}

	defer rows.Close()

	out := map[string]string{}

	for rows.Next() {
		var key, outcome string
		if err := rows.Scan(&key, &outcome); err != nil {
			t.Fatalf("scan: %v", err)
		}

		out[key] = outcome
	}

	return out
}

// park ends the workflow and waits for it.
func (h *harness) park(t *testing.T, run client.WorkflowRun) {
	t.Helper()

	if err := h.temporal.Client.SignalWorkflow(t.Context(), run.GetID(), "", workflows.ParkSignal,
		workflows.Park{Reason: workflows.ParkArchived, ClearFindings: true}); err != nil {
		t.Fatalf("park signal: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	if err := run.Get(ctx, nil); err != nil {
		t.Fatalf("workflow: %v", err)
	}
}

func eventually(t *testing.T, within time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v", within)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func TestIntegration_OneCheckWritesFindingsAndACheckRow(t *testing.T) {
	h := newHarness(t)
	w := h.startWorker(t, h.engine, "it-1")
	defer w.Stop()

	run := h.startRepoWorkflow(t)
	eventually(t, time.Minute, func() bool { return len(h.finalChecks(t)) == 1 })

	for key, outcome := range h.finalChecks(t) {
		if outcome != "success" {
			t.Errorf("check %s outcome = %q, want success", key, outcome)
		}
	}

	var status, reason string
	if err := h.pool.QueryRow(t.Context(),
		`SELECT status, reason FROM findings WHERE repository_id = $1 AND rule_kind = 'file' AND rule_name = 'codeowners'`,
		h.repoID).Scan(&status, &reason); err != nil {
		t.Fatalf("codeowners finding: %v", err)
	}

	if status != "non_compliant" || reason != "file_missing" {
		t.Errorf("codeowners = %s/%s, want non_compliant/file_missing", status, reason)
	}

	var providerID int64
	if err := h.pool.QueryRow(t.Context(), `SELECT provider_repo_id FROM repositories WHERE id = $1`, h.repoID).Scan(&providerID); err != nil {
		t.Fatalf("repository: %v", err)
	}

	if providerID != 9001 {
		t.Errorf("provider_repo_id = %d, want the check's 9001", providerID)
	}

	h.park(t, run)
	h.captureHistory(t, run.GetID(), run.GetRunID(), "check_then_park")
	h.captureHistory(t, workflows.InstallationWorkflowID(installationID), "", "installation_grant_report")
}

// captureHistory writes a workflow's history for the replay suite when
// -update-histories is set.
func (h *harness) captureHistory(t *testing.T, workflowID, runID, name string) {
	t.Helper()

	if !*updateHistories {
		return
	}

	hist := &historypb.History{}

	iter := h.temporal.Client.GetWorkflowHistory(t.Context(), workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		ev, err := iter.Next()
		if err != nil {
			t.Fatalf("history: %v", err)
		}

		hist.Events = append(hist.Events, ev)
	}

	b, err := temporalproto.CustomJSONMarshalOptions{Indent: "  "}.Marshal(hist)
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}

	path := filepath.Join("..", "workflows", "testdata", "histories", name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatalf("write history: %v", err)
	}
}

// blockingEngine blocks its first CheckRepo until the worker running it
// is stopped, simulating a worker killed mid-check.
type blockingEngine struct {
	activities.Engine

	calls   atomic.Int32
	started chan struct{}
}

func (b *blockingEngine) CheckRepo(ctx context.Context, c ghclient.Client, owner, repo string) (*checker.CheckResult, error) {
	if b.calls.Add(1) == 1 {
		close(b.started)
		<-ctx.Done()

		return nil, ctx.Err()
	}

	return b.Engine.CheckRepo(ctx, c, owner, repo)
}

func TestIntegration_WorkerKilledMidCheckLeavesOneFinalRow(t *testing.T) {
	h := newHarness(t)
	eng := &blockingEngine{Engine: h.engine, started: make(chan struct{})}

	first := h.startWorker(t, eng, "it-1")
	run := h.startRepoWorkflow(t)

	select {
	case <-eng.started:
	case <-time.After(time.Minute):
		t.Fatal("first check never started")
	}

	first.Stop()

	second := h.startWorker(t, eng, "it-1")
	defer second.Stop()

	eventually(t, 2*time.Minute, func() bool { return len(h.finalChecks(t)) == 1 })

	checks := h.finalChecks(t)
	for key, outcome := range checks {
		if outcome != "success" {
			t.Errorf("check %s outcome = %q, want success", key, outcome)
		}
	}

	var rows int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM checks WHERE repository_id = $1`, h.repoID).Scan(&rows); err != nil {
		t.Fatalf("count checks: %v", err)
	}

	if rows != 1 || eng.calls.Load() < 2 {
		t.Errorf("checks rows = %d after %d CheckRepo calls, want exactly 1 for the retried key", rows, eng.calls.Load())
	}

	h.park(t, run)
}
