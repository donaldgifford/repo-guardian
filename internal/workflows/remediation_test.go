package workflows

import (
	"context"
	"errors"
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

const testControl = "codeowners"

// remediationFakes records the remediation activities and answers them
// from scripts.
type remediationFakes struct {
	mu      sync.Mutex
	runs    []RemediateInput
	reads   int
	acquire []AcquireInput
	reports []Report

	// script answers the nth Remediate call; nil means Done from state.
	script func(n int) *RemediateResult
	// state answers the nth ReadRemediationState call; nil means the
	// state every run started from.
	state func(n int) RemediationState
	// during runs inside the nth Remediate call, to model signals that
	// arrive while a remediation is in flight.
	during func(n int)
}

func (f *remediationFakes) Remediate(_ context.Context, in *RemediateInput) (*RemediateResult, error) {
	f.mu.Lock()
	n := len(f.runs)
	f.runs = append(f.runs, *in)
	f.mu.Unlock()

	if f.during != nil {
		f.during(n)
	}

	if f.script != nil {
		return f.script(n), nil
	}

	return &RemediateResult{Kind: RemediationDone}, nil
}

func (f *remediationFakes) ReadRemediationState(_ context.Context, _ *ReadRemediationStateInput) (*RemediationState, error) {
	f.mu.Lock()
	n := f.reads
	f.reads++
	f.mu.Unlock()

	var s RemediationState
	if f.state != nil {
		s = f.state(n)
	}

	return &s, nil
}

func (f *remediationFakes) AcquireBudget(_ context.Context, in *AcquireInput) (*AcquireResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.acquire = append(f.acquire, *in)

	return &AcquireResult{Granted: true, LeaseID: in.UpdateID}, nil
}

func newRemediationEnv(t *testing.T, f *remediationFakes) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(RemediationWorkflow, workflow.RegisterOptions{Name: RemediationWorkflowName})
	env.RegisterActivityWithOptions(f.Remediate, activity.RegisterOptions{Name: RemediateActivity})
	env.RegisterActivityWithOptions(f.ReadRemediationState, activity.RegisterOptions{Name: ReadRemediationStateActivity})
	env.RegisterActivityWithOptions(f.AcquireBudget, activity.RegisterOptions{Name: AcquireBudgetActivity})
	env.OnSignalExternalWorkflow(mock.Anything, InstallationWorkflowID(AppRemediate, 7), "", ReportSignal, mock.Anything).
		Return(func(_, _, _, _ string, arg any) error {
			f.mu.Lock()
			defer f.mu.Unlock()

			if rep, ok := arg.(*Report); ok {
				f.reports = append(f.reports, *rep)
			}

			return nil
		}).Maybe()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: RemediationWorkflowID(testRepoID, testControl)})

	return env
}

func remediationInput() *RemediationInput {
	return &RemediationInput{RepositoryID: testRepoID, Control: testControl, InstallationID: 7}
}

func TestRemediationWorkflow_NoOpCompletes(t *testing.T) {
	t.Parallel()

	f := &remediationFakes{}
	env := newRemediationEnv(t, f)
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("completed %v, error %v; want a clean completion", env.IsWorkflowCompleted(), env.GetWorkflowError())
	}

	if len(f.runs) != 1 || f.reads != 1 {
		t.Fatalf("runs %d, reads %d; want one loop", len(f.runs), f.reads)
	}

	if a := f.acquire[0]; a.App != AppRemediate || a.InstallationID != 7 ||
		a.Request.Holder != BudgetHolder(AppRemediate, RemediationWorkflowID(testRepoID, testControl)) {
		t.Errorf("acquire = %+v, want the Remediation App's budget", a)
	}

	if in := f.runs[0]; in.RepositoryID != testRepoID || in.Control != testControl || in.OperationKey == "" {
		t.Errorf("Remediate input = %+v", in)
	}
}

func TestRemediationWorkflow_SignalDuringRunIsOneMoreLoop(t *testing.T) {
	t.Parallel()

	f := &remediationFakes{}
	env := newRemediationEnv(t, f)
	f.during = func(n int) {
		if n == 0 {
			// Two signals during one loop coalesce into one more loop.
			env.SignalWorkflow(RemediateSignal, Remediate{})
			env.SignalWorkflow(RemediateSignal, Remediate{})
		}
	}
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	if len(f.runs) != 2 {
		t.Fatalf("runs = %d, want 2 (one extra loop, not one per signal)", len(f.runs))
	}

	// The loop the signal forced skips the state read; the last one reads.
	if f.reads != 1 {
		t.Errorf("reads = %d, want 1", f.reads)
	}

	if f.runs[0].OperationKey == f.runs[1].OperationKey {
		t.Errorf("both loops ran under %q, want one key per loop", f.runs[0].OperationKey)
	}
}

func TestRemediationWorkflow_StateMovedLoopsAgain(t *testing.T) {
	t.Parallel()

	f := &remediationFakes{
		script: func(n int) *RemediateResult {
			return &RemediateResult{Kind: RemediationDone, Start: RemediationState{Generation: int64(n + 1)}}
		},
		// The first read sees the evaluator bump the generation past the
		// run's start; the second sees what the second loop started from.
		state: func(int) RemediationState { return RemediationState{Generation: 2} },
	}
	env := newRemediationEnv(t, f)
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	if len(f.runs) != 2 || f.reads != 2 {
		t.Fatalf("runs %d, reads %d; want 2, 2", len(f.runs), f.reads)
	}
}

func TestRemediationWorkflow_ThrottleDefersWithoutRecording(t *testing.T) {
	t.Parallel()

	f := &remediationFakes{script: func(n int) *RemediateResult {
		if n == 0 {
			return &RemediateResult{Kind: RemediationDeferred, Until: time.Now().Add(time.Hour)}
		}

		return &RemediateResult{Kind: RemediationDone}
	}}
	env := newRemediationEnv(t, f)
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	if len(f.runs) != 2 {
		t.Fatalf("runs = %d, want the deferred attempt and its retry", len(f.runs))
	}

	if first, retry := f.runs[0], f.runs[1]; retry.OperationKey != first.OperationKey || retry.Deferrals != 1 {
		t.Errorf("retry = %+v after %+v, want the same operation key with Deferrals 1", retry, first)
	}

	// A deferral reads no state and loops no further: nothing was recorded.
	if f.reads != 1 {
		t.Errorf("reads = %d, want 1 (after the retry only)", f.reads)
	}

	if len(f.reports) == 0 || f.reports[0].Remaining != 0 || f.reports[0].Reset.IsZero() {
		t.Errorf("first report = %+v, want remaining 0 until the reset", f.reports)
	}
}

func TestRemediationWorkflow_ContinuesAsNewAfterMaxLoops(t *testing.T) {
	t.Parallel()

	// The state always moves, so the run never settles.
	f := &remediationFakes{state: func(n int) RemediationState { return RemediationState{Generation: int64(n + 1)} }}
	env := newRemediationEnv(t, f)
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	var can *workflow.ContinueAsNewError
	if err := env.GetWorkflowError(); !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew", err)
	}

	if len(f.runs) != maxRemediationLoops {
		t.Errorf("runs = %d, want %d", len(f.runs), maxRemediationLoops)
	}
}

// TestRemediationWorkflow_BusyAttemptSettles is IMPL-0030 3.10 at the
// workflow: an attempt that found the operation claimed by an
// overlapping one returns busy, and the run settles on the state read
// instead of acting again.
func TestRemediationWorkflow_BusyAttemptSettles(t *testing.T) {
	t.Parallel()

	f := &remediationFakes{script: func(int) *RemediateResult { return &RemediateResult{Kind: RemediationBusy} }}
	env := newRemediationEnv(t, f)
	env.ExecuteWorkflow(RemediationWorkflowName, remediationInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	if len(f.runs) != 1 || f.reads != 1 {
		t.Errorf("runs %d, reads %d; want one busy loop, then settle", len(f.runs), f.reads)
	}
}
