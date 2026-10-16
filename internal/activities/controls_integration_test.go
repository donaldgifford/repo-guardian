//go:build integration

package activities_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/controls/builtin"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

const widgetsProviderID int64 = 9001

// fixtureLister is the Evaluation App's listing of the fixture org: one
// installation on acme holding acme/widgets.
type fixtureLister struct{}

func (fixtureLister) ListInstallations(context.Context) ([]*ghclient.Installation, error) {
	return []*ghclient.Installation{{ID: installationID, Account: "acme", RepositorySelection: "all"}}, nil
}

func (fixtureLister) ListInstallationRepos(context.Context, int64) ([]*ghclient.Repository, error) {
	return []*ghclient.Repository{{ID: widgetsProviderID, Owner: "acme", Name: "widgets", HasBranch: true, DefaultRef: "main"}}, nil
}

// controlsGitHub serves acme/widgets at a pinned head with no files, until
// lost is set, when the repository 404s: the Evaluation App lost it.
func controlsGitHub(t *testing.T, lost *atomic.Bool) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/acme/widgets", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if lost.Load() {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)

			return
		}

		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4321")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		w.Header().Set("X-RateLimit-Resource", "core")
		fmt.Fprintf(w, `{"id":%d,"name":"widgets","full_name":"acme/widgets","owner":{"login":"acme"},"default_branch":"main"}`, widgetsProviderID)
	})
	mux.HandleFunc("GET /api/v3/repos/acme/widgets/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ref":"refs/heads/main","object":{"type":"commit","sha":"0123456789abcdef0123456789abcdef01234567"}}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write from the Evaluation App: %s %s", r.Method, r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	})

	return mux
}

// writeControlsPolicy writes a policy root assigning codeowners@1 to acme
// and loads it.
func writeControlsPolicy(t *testing.T) *policy.Snapshot {
	t.Helper()

	root := t.TempDir()
	for name, body := range map[string]string{
		"enterprise.hcl": "enterprise {\n  orgs     = [\"acme\"]\n  controls = [\"codeowners@1\"]\n}\n",
		"catalogue/codeowners.hcl": "control \"codeowners\" {\n  version  = 1\n  type     = \"codeowners\"\n  title    = \"CODEOWNERS\"\n" +
			"  template = \"codeowners\"\n\n  rule \"exists\" {\n    title     = \"A CODEOWNERS file exists\"\n    remediate = true\n  }\n}\n",
	} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := policy.Load(root, builtin.Registry(), quiet)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}

	return snap
}

// controlsPool connects to dsn and closes the pool with the test.
func controlsPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

// startEvaluatorWorker runs the evaluator's activities and the controls
// workflows on the evaluation queue, as the evaluator deployment's
// current build. IMPL-0029 task 7.1 wires the same set into the role.
func startEvaluatorWorker(t *testing.T, ts *temporaltest.Server, st *postgres.ControlsStore, snap *policy.Snapshot, gh *ghclient.GitHubClient) {
	t.Helper()

	const buildID = "eval-it-1"

	queue := ts.Config.TaskQueue
	wc := temporal.WorkerConfig{TaskQueue: queue, Deployment: temporal.DeploymentEval, ActivityConcurrency: 2, BuildID: buildID}
	w := temporal.NewWorker(ts.Client, &wc)

	for name, fn := range map[string]any{
		workflows.InstallationWorkflowName:      workflows.InstallationWorkflow,
		workflows.EvaluationWorkflowName:        workflows.EvaluationWorkflow,
		workflows.ControlsDiscoveryWorkflowName: workflows.ControlsDiscoveryWorkflow,
	} {
		w.RegisterWorkflowWithOptions(fn, workflow.RegisterOptions{Name: name})
	}

	activities.NewControlsServices(&activities.ControlsServicesConfig{
		Store: st, GitHub: fixtureLister{}, Policy: snap, Temporal: ts.Client, TaskQueue: queue, Interval: 24 * time.Hour, Logger: quiet,
	}).Register(w)
	activities.NewEvaluator(st, snap, activities.NewGitHubEvalClients(factory{gh}), quiet).Register(w)
	activities.NewBudget(ts.Client, workflows.AppEval, queue, 0.10).Register(w)

	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	t.Cleanup(w.Stop)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if err := temporal.PromoteBuild(ctx, ts.Client, temporal.DeploymentEval, buildID, quiet); err != nil {
		t.Fatalf("promote build: %v", err)
	}
}

// TestIntegration_OneEvaluation is IMPL-0029 task 5.12: discovery →
// resolution → evaluation → recorded results for a fixture org, then the
// repository is lost and the EvaluationWorkflow parks and ends. Its
// history is the replay suite's evaluation_first_run.
func TestIntegration_OneEvaluation(t *testing.T) {
	ctx := t.Context()
	dsns := pgtest.ControlsDB(t)
	snap := writeControlsPolicy(t)

	ownerStore := postgres.NewControlsStore(controlsPool(t, dsns.Owner))
	if _, err := ownerStore.ActivatePolicyVersion(ctx, snap.Version(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("activate policy: %v", err)
	}

	st := postgres.NewControlsStore(controlsPool(t, dsns.Evaluator))
	owner := controlsPool(t, dsns.Owner)

	var lost atomic.Bool

	srv := httptest.NewServer(controlsGitHub(t, &lost))
	t.Cleanup(srv.Close)

	gh, err := ghclient.NewClientForBaseURL(srv.URL, srv.Client().Transport, quiet, 0.10)
	if err != nil {
		t.Fatalf("github client: %v", err)
	}

	ts := temporaltest.Start(t)
	startEvaluatorWorker(t, ts, st, snap, gh)

	disc, err := ts.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "controls-discovery", TaskQueue: ts.Config.TaskQueue,
	}, workflows.ControlsDiscoveryWorkflowName, &workflows.DiscoveryInput{})
	if err != nil {
		t.Fatalf("start discovery: %v", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if err := disc.Get(runCtx, nil); err != nil {
		t.Fatalf("discovery: %v", err)
	}

	var repoID int64
	if err := owner.QueryRow(ctx, `SELECT id FROM repositories WHERE provider_repo_id = $1`, widgetsProviderID).Scan(&repoID); err != nil {
		t.Fatalf("discovered repository: %v", err)
	}

	// Discovery spreads first evaluations across EVAL_INTERVAL; a schedule
	// re-check brings this one forward.
	evalID := workflows.EvaluationWorkflowID(repoID)
	if err := ts.Client.SignalWorkflow(ctx, evalID, "", workflows.RecheckSignal,
		&workflows.Recheck{Trigger: workflows.TriggerSchedule, Priority: workflows.PrioritySchedule, Unknown: true}); err != nil {
		t.Fatalf("recheck: %v", err)
	}

	var status, sha string

	eventually(t, time.Minute, func() bool {
		err := owner.QueryRow(ctx, `SELECT status, evaluated_sha FROM control_results WHERE repository_id = $1 AND control_id = 'codeowners'`,
			repoID).Scan(&status, &sha)

		return err == nil
	})

	if status != "non_compliant" || sha != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("codeowners result = %s at %s, want non_compliant at the pinned head", status, sha)
	}

	// The evaluator feeds rate_limit_remaining, which
	// RepoGuardianRateLimitNearExhaustion reads.
	if got := testutil.ToFloat64(metrics.RateLimitRemaining.WithLabelValues(strconv.FormatInt(installationID, 10))); got != 4321 {
		t.Errorf("rate_limit_remaining = %v, want the 4321 the fixture reported", got)
	}

	lost.Store(true)

	if err := ts.Client.SignalWorkflow(ctx, evalID, "", workflows.RecheckSignal,
		&workflows.Recheck{Trigger: workflows.TriggerSchedule, Priority: workflows.PrioritySchedule, Unknown: true}); err != nil {
		t.Fatalf("recheck: %v", err)
	}

	if err := ts.Client.GetWorkflow(ctx, evalID, "").Get(runCtx, nil); err != nil {
		t.Fatalf("EvaluationWorkflow: %v", err)
	}

	var active bool
	if err := owner.QueryRow(ctx, `SELECT active FROM repositories WHERE id = $1`, repoID).Scan(&active); err != nil || active {
		t.Errorf("repository active = %v (%v), want parked after the Evaluation App lost it", active, err)
	}

	h := &harness{temporal: ts}
	h.captureHistory(t, evalID, "", "evaluation_first_run")
}
