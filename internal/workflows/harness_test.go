package workflows

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const (
	testRepoID   int64 = 42
	testInterval       = 24 * time.Hour
)

// fakeActivities records every call and answers CheckRepo from a
// script. Registering them by name keeps the test honest about the
// name-only coupling between workflows and activities.
type fakeActivities struct {
	mu      sync.Mutex
	checks  []CheckRepoInput
	records []RecordCheckInput
	errors  []RecordCheckErrorInput
	parks   []ParkInput
	acquire []AcquireInput
	reports []Report

	// grant answers the nth AcquireBudget call; nil always grants.
	grant func(n int) *AcquireResult

	// attempts and times record each CheckRepo call's activity attempt
	// and the test clock when it ran.
	attempts []int32
	times    []time.Time
	now      func() time.Time

	// script answers the nth CheckRepo call (0-based); nil means Checked.
	script func(n int, in *CheckRepoInput) (*CheckRepoResult, error)
}

func (f *fakeActivities) CheckRepo(ctx context.Context, in *CheckRepoInput) (*CheckRepoResult, error) {
	f.mu.Lock()
	n := len(f.checks)
	f.checks = append(f.checks, *in)
	f.attempts = append(f.attempts, activity.GetInfo(ctx).Attempt)
	f.times = append(f.times, f.now())
	f.mu.Unlock()

	if f.script != nil {
		return f.script(n, in)
	}

	return &CheckRepoResult{Kind: CheckChecked, CheckKey: in.CheckKey}, nil
}

func (f *fakeActivities) AcquireBudget(_ context.Context, in *AcquireInput) (*AcquireResult, error) {
	f.mu.Lock()
	n := len(f.acquire)
	f.acquire = append(f.acquire, *in)
	f.mu.Unlock()

	if f.grant != nil {
		return f.grant(n), nil
	}

	return &AcquireResult{Granted: true, LeaseID: in.UpdateID}, nil
}

func (f *fakeActivities) RecordCheck(_ context.Context, in *RecordCheckInput) (*RecordCheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.records = append(f.records, *in)

	return &RecordCheckResult{}, nil
}

func (f *fakeActivities) RecordCheckError(_ context.Context, in *RecordCheckErrorInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.errors = append(f.errors, *in)

	return nil
}

func (f *fakeActivities) Park(_ context.Context, in *ParkInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.parks = append(f.parks, *in)

	return nil
}

// testEnv is the time-skipping environment the tests drive.
type testEnv = *testsuite.TestWorkflowEnvironment

// newEnv returns a time-skipping test environment running RepoWorkflow
// as repo/42 against fakes.
func newEnv(t *testing.T, fakes *fakeActivities) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(RepoWorkflow, workflow.RegisterOptions{Name: RepoWorkflowName})
	env.RegisterActivityWithOptions(fakes.CheckRepo, activity.RegisterOptions{Name: CheckRepoActivity})
	env.RegisterActivityWithOptions(fakes.RecordCheck, activity.RegisterOptions{Name: RecordCheckActivity})
	env.RegisterActivityWithOptions(fakes.RecordCheckError, activity.RegisterOptions{Name: RecordCheckErrorActivity})
	env.RegisterActivityWithOptions(fakes.Park, activity.RegisterOptions{Name: ParkActivity})
	env.RegisterActivityWithOptions(fakes.AcquireBudget, activity.RegisterOptions{Name: AcquireBudgetActivity})
	env.OnSignalExternalWorkflow(mock.Anything, InstallationWorkflowID(7), "", ReportSignal, mock.Anything).
		Return(func(_, _, _, _ string, arg any) error {
			fakes.mu.Lock()
			defer fakes.mu.Unlock()

			fakes.reports = append(fakes.reports, *arg.(*Report))

			return nil
		}).Maybe()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: RepoWorkflowID(testRepoID)})
	fakes.now = env.Now

	t.Cleanup(func() { env.AssertExpectations(t) })

	return env
}

func input() *RepoWorkflowInput {
	return &RepoWorkflowInput{RepositoryID: testRepoID, InstallationID: 7, CheckInterval: testInterval}
}

// parkAfter scripts n Checked results, then Parked, so a workflow ends.
func parkAfter(n int) func(int, *CheckRepoInput) (*CheckRepoResult, error) {
	return func(i int, in *CheckRepoInput) (*CheckRepoResult, error) {
		if i >= n {
			return &CheckRepoResult{Kind: CheckParked, CheckKey: in.CheckKey, ParkReason: ParkArchived, ClearFindings: true}, nil
		}

		return &CheckRepoResult{Kind: CheckChecked, CheckKey: in.CheckKey}, nil
	}
}
