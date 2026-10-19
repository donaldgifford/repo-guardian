package activities

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// fakeRemediationStore answers RemediationTarget from targets in turn
// (the last one repeats) and records holds.
type fakeRemediationStore struct {
	mu      sync.Mutex
	targets []*store.RemediationTarget
	reads   int
	holds   []string
}

func (f *fakeRemediationStore) RemediationTarget(context.Context, int64, string) (*store.RemediationTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.targets) == 0 {
		return nil, nil //nolint:nilnil // a withdrawn control
	}

	t := f.targets[min(f.reads, len(f.targets)-1)]
	f.reads++

	if t == nil {
		return nil, nil //nolint:nilnil // a withdrawn control
	}

	c := *t

	return &c, nil
}

func (f *fakeRemediationStore) SetHold(_ context.Context, _ int64, _ string, _ int64, hold string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.holds = append(f.holds, hold)

	return true, nil
}

type fakeRemediationClients struct{ opened int }

func (f *fakeRemediationClients) Repo(context.Context, int64, string, string) (RemediationRepo, error) {
	f.opened++

	return fakeRemediationRepo{}, nil
}

type fakeRemediationRepo struct{}

func (fakeRemediationRepo) Writer() control.Writer       { return nil }
func (fakeRemediationRepo) Reader(string) control.Reader { return nil }
func (fakeRemediationRepo) Observer() control.PRObserver { return nil }

// remediable is a target a run acts on.
func remediable() *store.RemediationTarget {
	return &store.RemediationTarget{
		Org: "acme", Name: "svc", Active: true, Epoch: 3, State: "active", EffectiveMode: "remediate", Revision: "r1",
		InstallationID: 7, Remediable: true, HasResult: true, ResultEpoch: 3, Generation: 5, IntentRevision: "i5",
	}
}

func remediateInput() *workflows.RemediateInput {
	return &workflows.RemediateInput{RepositoryID: 9, Control: "codeowners", InstallationID: 7, OperationKey: "remediation/9/codeowners/r/0"}
}

// runRemediate runs Remediate once in a test activity environment with
// act as the run's work.
func runRemediate(t *testing.T, st *fakeRemediationStore, act remediationAct) (*workflows.RemediateResult, error) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	r := NewRemediator(st, &fakeRemediationClients{}, ghclient.BotIdentity{}, slog.New(slog.DiscardHandler))
	r.act = act

	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(r.Remediate, activity.RegisterOptions{Name: workflows.RemediateActivity})

	val, err := env.ExecuteActivity(workflows.RemediateActivity, remediateInput())
	if err != nil {
		return nil, err
	}

	var res workflows.RemediateResult
	if err := val.Get(&res); err != nil {
		t.Fatal(err)
	}

	return &res, nil
}

// TestRemediate_OperationHeldExitsCleanly is IMPL-0030 3.3: a second
// attempt that finds the operation claimed does nothing and returns a
// result, so the retry policy never sees it.
func TestRemediate_OperationHeldExitsCleanly(t *testing.T) {
	t.Parallel()

	st := &fakeRemediationStore{targets: []*store.RemediationTarget{remediable()}}

	res, err := runRemediate(t, st, func(context.Context, *remediationRun) error {
		return store.ErrOperationHeld
	})
	if err != nil {
		t.Fatalf("Remediate = %v, want a clean result", err)
	}

	if res.Kind != workflows.RemediationBusy || len(st.holds) != 0 {
		t.Errorf("Remediate = %+v, holds %v; want busy and nothing recorded", res, st.holds)
	}

	if res.Start != (workflows.RemediationState{Generation: 5, IntentRevision: "i5"}) {
		t.Errorf("start state = %+v, want the target's", res.Start)
	}
}
