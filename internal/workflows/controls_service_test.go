package workflows

import (
	"context"
	"testing"
	"time"

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

// rolloutFakes answers the rollout's paged activities with pages pages
// of 10 and records every call.
type rolloutFakes struct {
	*serviceFakes

	resolves, signalsIn []ControlsPageInput
	completedAt         time.Time
}

func (f *rolloutFakes) pageOf(in *ControlsPageInput) *ControlsPageResult {
	page := int(in.AfterID / 10)

	return &ControlsPageResult{NextAfterID: in.AfterID + 10, Count: 10, Done: page+1 >= f.pages}
}

func (f *rolloutFakes) ResolveAll(_ context.Context, in *ControlsPageInput) (*ControlsPageResult, error) {
	f.resolves = append(f.resolves, *in)

	return f.pageOf(in), nil
}

func (f *rolloutFakes) SignalAll(_ context.Context, in *ControlsPageInput) (*ControlsPageResult, error) {
	f.signalsIn = append(f.signalsIn, *in)

	return f.pageOf(in), nil
}

func (f *rolloutFakes) Complete(_ context.Context, v string) error {
	f.completed, f.completedAt = append(f.completed, v), f.now()

	return nil
}

func TestControlsRolloutWorkflow_ResolvesSignalsThenCompletes(t *testing.T) {
	t.Parallel()

	f := &rolloutFakes{serviceFakes: &serviceFakes{pages: 2}}

	var s testsuite.WorkflowTestSuite

	env := s.NewTestWorkflowEnvironment()
	f.now = env.Now
	start := env.Now()

	for name, fn := range map[string]any{
		ControlsResolveAllActivity:       f.ResolveAll,
		ControlsSignalAllActivity:        f.SignalAll,
		ControlsCompleteRolloutActivity:  f.Complete,
		ControlsRecordServiceRunActivity: f.RecordServiceRun,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	env.ExecuteWorkflow(ControlsRolloutWorkflow, &ControlsRolloutInput{Version: "controls:abc", Window: 6 * time.Hour})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.resolves) != 2 || len(f.signalsIn) != 2 || f.signalsIn[1].AfterID != 10 {
		t.Errorf("resolve pages %d, signal pages %d; want two of each, paged by AfterID", len(f.resolves), len(f.signalsIn))
	}

	if by := f.signalsIn[0].By; !by.Equal(start.Add(6*time.Hour)) || f.signalsIn[0].Version != "controls:abc" {
		t.Errorf("signal pass = %+v, want policy_changed for controls:abc by the end of the window", f.signalsIn[0])
	}

	if len(f.completed) != 1 || f.completedAt.Before(start.Add(6*time.Hour)) {
		t.Errorf("completed %v at %v, want once after the window", f.completed, f.completedAt)
	}

	if len(f.runs) != 1 || f.runs[0].Kind != ServicePolicyRollout || f.runs[0].Detail["signalled"] != float64(20) {
		t.Errorf("runs = %+v, want one policy_rollout run that signalled 20", f.runs)
	}
}

func TestControlsSnapshotWorkflow_WritesAndRecords(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{}

	var s testsuite.WorkflowTestSuite

	env := s.NewTestWorkflowEnvironment()
	start := env.Now()

	var stampedAt time.Time

	env.RegisterActivityWithOptions(func(_ context.Context, at time.Time) (int, error) {
		stampedAt = at

		return 4, nil
	}, activity.RegisterOptions{Name: ControlsSnapshotActivity})
	env.RegisterActivityWithOptions(f.RecordServiceRun, activity.RegisterOptions{Name: ControlsRecordServiceRunActivity})

	env.ExecuteWorkflow(ControlsSnapshotWorkflow, &SnapshotInput{Retention: time.Hour})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if !stampedAt.Equal(start) {
		t.Errorf("snapshot stamped %v, want the workflow's start %v", stampedAt, start)
	}

	if len(f.runs) != 1 || f.runs[0].Kind != ServiceSnapshot || f.runs[0].Detail["rows"] != float64(4) {
		t.Errorf("runs = %+v, want one snapshot run of 4 rows", f.runs)
	}
}
