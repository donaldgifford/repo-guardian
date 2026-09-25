package workflows

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// serviceFakes records the service activities. repos is what
// ListRepositories returns per installation; listErr fails it; pages is
// how many SignalRepositories pages each pass has.
type serviceFakes struct {
	mu sync.Mutex

	installations []InstallationRef
	repos         map[int64][]WebhookRepo
	listErr       map[int64]bool
	pages         int

	batches   []UpsertRepositoriesInput
	parked    []ParkMissingInput
	signals   []SignalRepositoriesInput
	signalAt  []time.Time
	snapshots []time.Time
	prunes    []time.Time
	completed []string
	cleared   int
	runs      []ServiceRun

	now func() time.Time
}

func (f *serviceFakes) ListInstallations(context.Context) ([]InstallationRef, error) {
	return f.installations, nil
}

func (f *serviceFakes) ListRepositories(_ context.Context, id int64) ([]WebhookRepo, error) {
	if f.listErr[id] {
		return nil, temporal.NewNonRetryableApplicationError("listing failed", "test", nil)
	}

	return f.repos[id], nil
}

func (f *serviceFakes) UpsertRepositories(_ context.Context, in *UpsertRepositoriesInput) (*UpsertRepositoriesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.batches = append(f.batches, *in)

	res := &UpsertRepositoriesResult{}
	for _, r := range in.Repos {
		res.IDs = append(res.IDs, r.ID)
	}

	res.Started = len(in.Repos) / 2

	return res, nil
}

func (f *serviceFakes) ParkMissing(_ context.Context, in *ParkMissingInput) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.parked = append(f.parked, *in)

	return 0, nil
}

func (f *serviceFakes) SignalRepositories(_ context.Context, in *SignalRepositoriesInput) (*SignalRepositoriesResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.signals = append(f.signals, *in)
	f.signalAt = append(f.signalAt, f.now())

	// Each pass is f.pages pages of 10; the page number is AfterID/10.
	page := int(in.AfterID / 10)

	return &SignalRepositoriesResult{NextAfterID: in.AfterID + 10, Signalled: 10, Done: page+1 >= f.pages}, nil
}

func (f *serviceFakes) InsertComplianceSnapshot(_ context.Context, at time.Time) (int, error) {
	f.snapshots = append(f.snapshots, at)

	return 3, nil
}

func (f *serviceFakes) PruneChecks(_ context.Context, before time.Time) (int64, error) {
	f.prunes = append(f.prunes, before)

	return 7, nil
}

func (f *serviceFakes) CompletePolicyRollout(_ context.Context, v string) error {
	f.completed = append(f.completed, v)

	return nil
}

func (f *serviceFakes) ClearBootstrapPending(context.Context) error {
	f.cleared++

	return nil
}

func (f *serviceFakes) RecordServiceRun(_ context.Context, run *ServiceRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs = append(f.runs, *run)

	return nil
}

func newServiceEnv(t *testing.T, f *serviceFakes) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var s testsuite.WorkflowTestSuite

	env := s.NewTestWorkflowEnvironment()
	f.now = env.Now

	for name, fn := range map[string]any{
		ListInstallationsActivity:     f.ListInstallations,
		ListRepositoriesActivity:      f.ListRepositories,
		UpsertRepositoriesActivity:    f.UpsertRepositories,
		ParkMissingActivity:           f.ParkMissing,
		SignalRepositoriesActivity:    f.SignalRepositories,
		SnapshotActivity:              f.InsertComplianceSnapshot,
		PruneChecksActivity:           f.PruneChecks,
		CompletePolicyRolloutActivity: f.CompletePolicyRollout,
		ClearBootstrapActivity:        f.ClearBootstrapPending,
		RecordServiceRunActivity:      f.RecordServiceRun,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	return env
}

func repos(n int) []WebhookRepo {
	out := make([]WebhookRepo, n)
	for i := range out {
		out[i] = WebhookRepo{ID: int64(i + 1), Org: "acme", Name: "r"}
	}

	return out
}

func TestDiscoveryWorkflow_CompleteListingBatchesAndParks(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{
		installations: []InstallationRef{{ID: 7}, {ID: 8}},
		repos:         map[int64][]WebhookRepo{7: repos(250), 8: repos(3)},
	}
	env := newServiceEnv(t, f)
	env.ExecuteWorkflow(DiscoveryWorkflow, &DiscoveryInput{})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	sizes := make([]int, 0, len(f.batches))
	for _, b := range f.batches {
		sizes = append(sizes, len(b.Repos))
	}

	if len(sizes) != 4 || sizes[0] != 100 || sizes[1] != 100 || sizes[2] != 50 || sizes[3] != 3 {
		t.Errorf("batch sizes = %v, want [100 100 50 3]", sizes)
	}

	if len(f.parked) != 2 || len(f.parked[0].Seen) != 250 || f.parked[1].InstallationID != 8 {
		t.Errorf("parks = %d calls, want one per installation with every seen id", len(f.parked))
	}

	if len(f.runs) != 1 || !f.runs[0].Success || f.runs[0].Kind != ServiceDiscovery || f.runs[0].Detail["repositories"] != float64(253) {
		t.Errorf("service run = %+v", f.runs)
	}
}

// A failed listing parks nothing for that installation: an incomplete
// listing cannot tell a removed repository from an unlisted one.
func TestDiscoveryWorkflow_FailedListingParksNothing(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{
		installations: []InstallationRef{{ID: 7}, {ID: 8}},
		repos:         map[int64][]WebhookRepo{8: repos(3)},
		listErr:       map[int64]bool{7: true},
	}
	env := newServiceEnv(t, f)
	env.ExecuteWorkflow(DiscoveryWorkflow, &DiscoveryInput{})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.parked) != 1 || f.parked[0].InstallationID != 8 {
		t.Errorf("parks = %+v, want only installation 8's", f.parked)
	}

	if len(f.runs) != 1 || f.runs[0].Success {
		t.Errorf("service run = %+v, want an unsuccessful run", f.runs)
	}
}

func TestDiscoveryWorkflow_SingleInstallationSkipsListingInstallations(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{repos: map[int64][]WebhookRepo{7: repos(2)}}
	env := newServiceEnv(t, f)
	env.ExecuteWorkflow(DiscoveryWorkflow, &DiscoveryInput{InstallationID: 7})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.batches) != 1 || f.batches[0].InstallationID != 7 || len(f.parked) != 1 {
		t.Errorf("batches = %+v, parks = %+v", f.batches, f.parked)
	}
}

func TestSnapshotWorkflow_SnapshotsThenPrunes(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{}
	env := newServiceEnv(t, f)
	start := env.Now()
	env.ExecuteWorkflow(SnapshotWorkflow, &SnapshotInput{Retention: 90 * 24 * time.Hour})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.snapshots) != 1 || len(f.prunes) != 1 || !f.prunes[0].Equal(f.snapshots[0].Add(-90*24*time.Hour)) ||
		f.snapshots[0].Before(start) {
		t.Errorf("snapshots = %v, prunes = %v", f.snapshots, f.prunes)
	}

	if len(f.runs) != 1 || f.runs[0].Kind != ServiceSnapshot || f.runs[0].Detail["pruned_checks"] != float64(7) {
		t.Errorf("service run = %+v", f.runs)
	}
}

func TestPolicyRolloutWorkflow_SignalsEveryPageThenStragglersAtWindowEnd(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{pages: 3}
	env := newServiceEnv(t, f)
	start := env.Now()
	window := 24 * time.Hour
	env.ExecuteWorkflow(PolicyRolloutWorkflow, &PolicyRolloutInput{Version: "v2:abc", Window: window})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.signals) != 6 {
		t.Fatalf("SignalRepositories calls = %d, want 3 pages twice", len(f.signals))
	}

	for i, in := range f.signals[:3] {
		if in.DriftedOnly || in.Version != "v2:abc" || !in.By.Equal(start.Add(window)) || in.Limit != signalPage {
			t.Errorf("rollout page %d = %+v", i, in)
		}
	}

	for i, in := range f.signals[3:] {
		if !in.DriftedOnly || f.signalAt[3+i].Before(start.Add(window)) {
			t.Errorf("straggler page %d = %+v at %v, want drifted-only after the window", i, in, f.signalAt[3+i])
		}
	}

	if len(f.completed) != 1 || f.completed[0] != "v2:abc" {
		t.Errorf("completed = %v", f.completed)
	}

	if len(f.runs) != 1 || f.runs[0].Detail["signalled"] != float64(30) {
		t.Errorf("service run = %+v", f.runs)
	}
}

func TestBootstrapWorkflow_PagesAndClearsTheFlag(t *testing.T) {
	t.Parallel()

	f := &serviceFakes{pages: 2}
	env := newServiceEnv(t, f)
	env.ExecuteWorkflow(BootstrapWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.signals) != 2 || !f.signals[0].Bootstrap || f.signals[1].AfterID != 10 {
		t.Errorf("signals = %+v", f.signals)
	}

	if f.cleared != 1 || len(f.runs) != 1 || f.runs[0].Kind != ServiceBootstrap {
		t.Errorf("cleared = %d, runs = %+v", f.cleared, f.runs)
	}
}

func TestChunk(t *testing.T) {
	t.Parallel()

	var got []int
	for c := range chunk(make([]int, 7), 3) {
		got = append(got, len(c))
	}

	if len(got) != 3 || got[2] != 1 {
		t.Errorf("chunks = %v", got)
	}

	for range chunk([]int(nil), 3) {
		t.Error("empty input yielded a chunk")
	}
}
