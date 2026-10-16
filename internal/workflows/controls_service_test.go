package workflows

import (
	"context"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// controlsServiceFakes adapts serviceFakes to the controls activities'
// payloads, recording the controls batches.
type controlsServiceFakes struct {
	*serviceFakes

	controlsBatches []ControlsUpsertInput
}

func (f *controlsServiceFakes) ListRepositories(_ context.Context, id int64) ([]DiscoveredRepo, error) {
	if f.listErr[id] {
		return nil, temporal.NewNonRetryableApplicationError("listing failed", "test", nil)
	}

	out := make([]DiscoveredRepo, 0, len(f.repos[id]))
	for _, r := range f.repos[id] {
		out = append(out, DiscoveredRepo{ID: r.ID, Org: r.Org, Name: r.Name, Archived: r.ID == 1})
	}

	return out, nil
}

func (f *controlsServiceFakes) Upsert(_ context.Context, in *ControlsUpsertInput) (*UpsertRepositoriesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.controlsBatches = append(f.controlsBatches, *in)

	res := &UpsertRepositoriesResult{Started: len(in.Repos) / 2}
	for _, r := range in.Repos {
		res.IDs = append(res.IDs, r.ID)
	}

	return res, nil
}

func newControlsServiceEnv(t *testing.T, f *controlsServiceFakes) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var s testsuite.WorkflowTestSuite

	env := s.NewTestWorkflowEnvironment()
	f.now = env.Now

	for name, fn := range map[string]any{
		ControlsListInstallationsActivity: f.ListInstallations,
		ControlsListRepositoriesActivity:  f.ListRepositories,
		ControlsUpsertActivity:            f.Upsert,
		ControlsParkMissingActivity:       f.ParkMissing,
		ControlsRecordServiceRunActivity:  f.RecordServiceRun,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	return env
}

func TestControlsDiscoveryWorkflow_BatchesParksAndRecords(t *testing.T) {
	t.Parallel()

	f := &controlsServiceFakes{serviceFakes: &serviceFakes{
		installations: []InstallationRef{{ID: 7}, {ID: 8}},
		repos:         map[int64][]WebhookRepo{7: repos(150), 8: repos(3)},
	}}
	env := newControlsServiceEnv(t, f)
	env.ExecuteWorkflow(ControlsDiscoveryWorkflow, &DiscoveryInput{})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.controlsBatches) != 3 || len(f.controlsBatches[0].Repos) != 100 || !f.controlsBatches[0].Repos[0].Archived {
		t.Errorf("batches = %d, want [100 50 3] with archived repositories kept for resolution to park", len(f.controlsBatches))
	}

	if len(f.parked) != 2 || len(f.parked[0].Seen) != 150 {
		t.Errorf("parks = %+v, want one per installation with every seen id", f.parked)
	}

	if len(f.runs) != 1 || f.runs[0].Kind != ServiceDiscovery || !f.runs[0].Success || f.runs[0].Detail["repositories"] != float64(153) {
		t.Errorf("runs = %+v, want one successful discovery run over 153 repositories", f.runs)
	}
}

func TestControlsDiscoveryWorkflow_FailedListingParksNothing(t *testing.T) {
	t.Parallel()

	f := &controlsServiceFakes{serviceFakes: &serviceFakes{
		installations: []InstallationRef{{ID: 7}, {ID: 8}},
		repos:         map[int64][]WebhookRepo{8: repos(3)},
		listErr:       map[int64]bool{7: true},
	}}
	env := newControlsServiceEnv(t, f)
	env.ExecuteWorkflow(ControlsDiscoveryWorkflow, &DiscoveryInput{})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.parked) != 1 || f.parked[0].InstallationID != 8 {
		t.Errorf("parks = %+v, want installation 8 only", f.parked)
	}

	if len(f.runs) != 1 || f.runs[0].Success {
		t.Errorf("runs = %+v, want one unsuccessful run", f.runs)
	}
}
