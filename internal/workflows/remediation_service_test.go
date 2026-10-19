package workflows

import (
	"context"
	"slices"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// TestRemediationSweepWorkflow_MaintainsThenSweeps is IMPL-0030 6.1 and
// 6.2: maintenance first, with the configured batch, then the sweep;
// one service_runs row each.
func TestRemediationSweepWorkflow_MaintainsThenSweeps(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{}

	var (
		s     testsuite.WorkflowTestSuite
		order []string
	)

	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, batch int) (*MaintenanceResult, error) {
		order = append(order, "maintain")

		return &MaintenanceResult{Expired: batch, Closed: 2}, nil
	}, activity.RegisterOptions{Name: RemediationMaintainActivity})
	env.RegisterActivityWithOptions(func(_ context.Context, batch int) (*SweepResult, error) {
		order = append(order, "sweep")

		return &SweepResult{Due: batch, Started: 3}, nil
	}, activity.RegisterOptions{Name: RemediationSweepActivity})
	env.RegisterActivityWithOptions(f.RecordServiceRun, activity.RegisterOptions{Name: ControlsRecordServiceRunActivity})

	env.ExecuteWorkflow(RemediationSweepWorkflow, &RemediationSweepInput{SweepBatch: 50, MaintenanceBatch: 20})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(order, []string{"maintain", "sweep"}) {
		t.Errorf("order = %v, want maintenance before the sweep", order)
	}

	if len(f.runs) != 2 || f.runs[0].Kind != ServiceMaintenance || f.runs[0].Detail["expired"] != float64(20) ||
		f.runs[1].Kind != ServiceSweep || f.runs[1].Detail["due"] != float64(50) || f.runs[1].Detail["started"] != float64(3) {
		t.Errorf("runs = %+v, want a maintenance run then a sweep run", f.runs)
	}
}

// discoveryFakes are the remediator's discovery activities.
type discoveryFakes struct {
	installations []RemediateInstallation
	changes       map[int64][]WebhookRepo
	listErr       map[int64]bool
	pruned        [][]int64
	resolved      []ResolveAccessInput
}

func (f *discoveryFakes) env(t *testing.T, runs *serviceFakes) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var s testsuite.WorkflowTestSuite

	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(func(context.Context) ([]RemediateInstallation, error) {
		return f.installations, nil
	}, activity.RegisterOptions{Name: RemediationListInstallationsActivity})
	env.RegisterActivityWithOptions(func(_ context.Context, in *RemediateInstallation) ([]WebhookRepo, error) {
		if f.listErr[in.ID] {
			return nil, temporal.NewNonRetryableApplicationError("listing failed", "test", nil)
		}

		return f.changes[in.ID], nil
	}, activity.RegisterOptions{Name: RemediationRefreshAccessActivity})
	env.RegisterActivityWithOptions(func(_ context.Context, seen []int64) ([]WebhookRepo, error) {
		f.pruned = append(f.pruned, seen)

		return []WebhookRepo{{ID: 99, Org: "gone", Name: "repo"}}, nil
	}, activity.RegisterOptions{Name: RemediationPruneInstallationsActivity})
	env.RegisterActivityWithOptions(func(_ context.Context, in *ResolveAccessInput) error {
		f.resolved = append(f.resolved, *in)

		return nil
	}, activity.RegisterOptions{Name: RemediationResolveAccessActivity})
	env.RegisterActivityWithOptions(runs.RecordServiceRun, activity.RegisterOptions{Name: ControlsRecordServiceRunActivity})

	return env
}

// TestRemediationDiscoveryWorkflow is IMPL-0030 6.4: every installation
// is refreshed, a change is handed to the evaluator for re-resolution,
// an unchanged installation hands nothing, and a complete listing
// prunes the installations it did not see.
func TestRemediationDiscoveryWorkflow(t *testing.T) {
	t.Parallel()

	f := &discoveryFakes{
		installations: []RemediateInstallation{{ID: 1, Account: "acme"}, {ID: 2, Account: "globex"}},
		changes:       map[int64][]WebhookRepo{1: {{ID: 10, Org: "acme", Name: "api"}}},
	}
	runs := &serviceFakes{}

	env := f.env(t, runs)
	env.ExecuteWorkflow(RemediationDiscoveryWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.resolved) != 2 || f.resolved[0].InstallationID != 1 || f.resolved[0].AccountLogin != "acme" ||
		f.resolved[1].InstallationID != 0 || f.resolved[1].Repos[0].ID != 99 {
		t.Errorf("resolved = %+v, want installation 1's change, then the prune's", f.resolved)
	}

	if len(f.pruned) != 1 || !slices.Equal(f.pruned[0], []int64{1, 2}) {
		t.Errorf("pruned = %v, want one prune keeping installations 1 and 2", f.pruned)
	}

	if len(runs.runs) != 1 || runs.runs[0].Kind != ServiceRemediationDiscovery || !runs.runs[0].Success ||
		runs.runs[0].Detail["changed"] != float64(2) {
		t.Errorf("runs = %+v, want one successful remediation_discovery run of 2 changes", runs.runs)
	}
}

// TestRemediationDiscoveryWorkflow_IncompleteListingPrunesNothing: a
// failed listing records an incomplete run and prunes no installation.
func TestRemediationDiscoveryWorkflow_IncompleteListingPrunesNothing(t *testing.T) {
	t.Parallel()

	f := &discoveryFakes{
		installations: []RemediateInstallation{{ID: 1, Account: "acme"}, {ID: 2, Account: "globex"}},
		listErr:       map[int64]bool{2: true},
	}
	runs := &serviceFakes{}

	env := f.env(t, runs)
	env.ExecuteWorkflow(RemediationDiscoveryWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.pruned) != 0 || len(f.resolved) != 0 {
		t.Errorf("pruned %v, resolved %+v; want neither after an incomplete listing", f.pruned, f.resolved)
	}

	if len(runs.runs) != 1 || runs.runs[0].Success {
		t.Errorf("runs = %+v, want one unsuccessful run", runs.runs)
	}
}
