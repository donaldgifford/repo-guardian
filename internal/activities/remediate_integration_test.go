//go:build integration

package activities_test

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/github/ghfake"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// remediableStore answers every RemediationTarget with one remediable
// assignment; the store's own behaviour is pinned by the postgres
// integration tests.
type remediableStore struct{}

func (remediableStore) RemediationTarget(context.Context, int64, string) (*store.RemediationTarget, error) {
	return &store.RemediationTarget{
		Org: "acme", Name: "svc", Active: true, Epoch: 1, State: "active", EffectiveMode: "remediate", Revision: "r1",
		InstallationID: installationID, Remediable: true, HasResult: true, ResultEpoch: 1, Generation: 1, IntentRevision: "i1",
	}, nil
}

func (remediableStore) SetHold(context.Context, int64, string, int64, string) (bool, error) {
	return true, nil
}

// fakeClients opens every repository on one ghfake repository.
type fakeClients struct {
	repo  *ghfake.Repo
	actor ghfake.Actor
}

func (f fakeClients) Repo(context.Context, int64, string, string) (activities.RemediationRepo, error) {
	return fakeRepo{f.repo.As(f.actor)}, nil
}

type fakeRepo struct{ c *ghfake.Client }

func (r fakeRepo) Writer() control.Writer           { return r.c }
func (r fakeRepo) Reader(sha string) control.Reader { return r.c.At(sha) }
func (r fakeRepo) Observer() control.PRObserver     { return r.c }

// TestIntegration_OneRemediation is IMPL-0030 3.9's first behaviour
// test: a remediation started the way the evaluator starts one runs to
// completion on the remediator's queue and deployment against the
// GitHub fake with the no-op remediation, writing nothing. Its history
// is the replay suite's remediation_first_run.
func TestIntegration_OneRemediation(t *testing.T) {
	srv := temporaltest.Start(t)

	const (
		queue   = "repo-guardian-remediate-it"
		buildID = "remediate-it-1"
	)

	gh := ghfake.New()
	bot := ghfake.Actor{ID: 42, Login: "rg-remediate[bot]", Type: ghfake.TypeBot}
	repo := gh.AddRepo("acme", "svc", 100, map[string]string{"README.md": "hi\n"})

	wc := temporal.WorkerConfig{TaskQueue: queue, Deployment: temporal.DeploymentRemediate, ActivityConcurrency: 2, BuildID: buildID}
	w := temporal.NewWorker(srv.Client, &wc)

	for name, fn := range map[string]any{
		workflows.InstallationWorkflowName: workflows.InstallationWorkflow,
		workflows.RemediationWorkflowName:  workflows.RemediationWorkflow,
	} {
		w.RegisterWorkflowWithOptions(fn, workflow.RegisterOptions{Name: name})
	}

	activities.NewBudget(srv.Client, workflows.AppRemediate, queue, 0.10).Register(w)
	activities.NewRemediator(remediableStore{}, fakeClients{repo: repo, actor: bot}, ghclient.BotIdentity{ID: bot.ID, Login: bot.Login}, quiet).Register(w)

	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	t.Cleanup(w.Stop)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if err := temporal.PromoteBuild(ctx, srv.Client, temporal.DeploymentRemediate, buildID, quiet); err != nil {
		t.Fatalf("promote build: %v", err)
	}

	start := []workflows.RemediationStart{{RepositoryID: 9, Control: "codeowners", InstallationID: installationID, Priority: workflows.PrioritySchedule}}
	if err := activities.NewRemediationStarter(srv.Client, queue, nil).StartRemediations(ctx, start); err != nil {
		t.Fatalf("start remediation: %v", err)
	}

	id := workflows.RemediationWorkflowID(9, "codeowners")

	if err := srv.Client.GetWorkflow(ctx, id, "").Get(ctx, nil); err != nil {
		t.Fatalf("RemediationWorkflow: %v", err)
	}

	desc, err := srv.Client.DescribeWorkflowExecution(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}

	if got := desc.GetWorkflowExecutionInfo().GetTaskQueue(); got != queue {
		t.Errorf("ran on %q, want %q", got, queue)
	}

	if reqs := gh.Requests(); len(reqs) != 0 {
		t.Errorf("the no-op remediation made GitHub requests: %+v", reqs)
	}

	captureHistory(t, srv, id, "", "remediation_first_run")
}
