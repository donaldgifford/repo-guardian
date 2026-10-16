package workflows

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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

// evalFakes records the controls activities and answers Evaluate from a
// script.
type evalFakes struct {
	mu      sync.Mutex
	evals   []EvaluateInput
	records []RecordEvaluationInput
	parks   []ParkRepositoryInput
	acquire []AcquireInput

	// script answers the nth Evaluate call; nil means Checked.
	script func(n int, in *EvaluateInput) *EvaluateResult
	// during runs inside the nth Evaluate call, to model signals that
	// arrive while an evaluation is in flight.
	during func(n int)
}

func (f *evalFakes) Evaluate(_ context.Context, in *EvaluateInput) (*EvaluateResult, error) {
	f.mu.Lock()
	n := len(f.evals)
	f.evals = append(f.evals, *in)
	f.mu.Unlock()

	if f.during != nil {
		f.during(n)
	}

	if f.script != nil {
		return f.script(n, in), nil
	}

	return &EvaluateResult{Kind: CheckChecked}, nil
}

func (f *evalFakes) RecordEvaluation(_ context.Context, in *RecordEvaluationInput) (*RecordEvaluationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.records = append(f.records, *in)

	return &RecordEvaluationResult{}, nil
}

func (f *evalFakes) ParkRepository(_ context.Context, in *ParkRepositoryInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.parks = append(f.parks, *in)

	return nil
}

func (f *evalFakes) AcquireBudget(_ context.Context, in *AcquireInput) (*AcquireResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.acquire = append(f.acquire, *in)

	return &AcquireResult{Granted: true, LeaseID: in.UpdateID}, nil
}

func newEvalEnv(t *testing.T, f *evalFakes) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite

	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))

	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(EvaluationWorkflow, workflow.RegisterOptions{Name: EvaluationWorkflowName})
	env.RegisterActivityWithOptions(f.Evaluate, activity.RegisterOptions{Name: EvaluateActivity})
	env.RegisterActivityWithOptions(f.RecordEvaluation, activity.RegisterOptions{Name: RecordEvaluationActivity})
	env.RegisterActivityWithOptions(f.ParkRepository, activity.RegisterOptions{Name: ParkRepositoryActivity})
	env.RegisterActivityWithOptions(f.AcquireBudget, activity.RegisterOptions{Name: AcquireBudgetActivity})
	env.OnSignalExternalWorkflow(mock.Anything, InstallationWorkflowID(AppEval, 7), "", ReportSignal, mock.Anything).Return(nil).Maybe()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: EvaluationWorkflowID(testRepoID)})

	return env
}

func evalInput() *EvaluationWorkflowInput {
	return &EvaluationWorkflowInput{RepositoryID: testRepoID, InstallationID: 7, Interval: testInterval}
}

// parkEvalAfter scripts n Checked evaluations, then access loss.
func parkEvalAfter(n int) func(int, *EvaluateInput) *EvaluateResult {
	return func(i int, _ *EvaluateInput) *EvaluateResult {
		if i >= n {
			return &EvaluateResult{Kind: CheckParked, ParkReason: ParkAccessDenied}
		}

		return &EvaluateResult{Kind: CheckChecked}
	}
}

func TestEvaluationWorkflow_ScheduleRecordsAndParks(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: parkEvalAfter(2)}
	env := newEvalEnv(t, f)
	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	if len(f.evals) != 3 || len(f.records) != 2 || len(f.parks) != 1 {
		t.Fatalf("evals %d, records %d, parks %d; want 3, 2, 1", len(f.evals), len(f.records), len(f.parks))
	}

	if in := f.evals[0]; !in.All || !in.Scheduled || in.Trigger != TriggerSchedule {
		t.Errorf("scheduled evaluation input = %+v, want every control including API resources", in)
	}

	if a := f.acquire[0]; a.App != AppEval || a.Request.Holder != "eval/"+EvaluationWorkflowID(testRepoID) {
		t.Errorf("acquire = %+v, want the Evaluation App's budget", a)
	}
}

func TestEvaluationWorkflow_DeferralRecordsNothing(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: func(n int, _ *EvaluateInput) *EvaluateResult {
		switch n {
		case 0:
			return &EvaluateResult{Kind: CheckDeferred, Until: time.Now().Add(time.Hour)}
		case 1:
			return &EvaluateResult{Kind: CheckChecked}
		default:
			return &EvaluateResult{Kind: CheckParked, ParkReason: ParkAccessDenied}
		}
	}}
	env := newEvalEnv(t, f)
	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.records) != 1 || f.evals[0].CheckKey != f.evals[1].CheckKey || f.evals[1].Deferrals != 1 {
		t.Errorf("records %d, keys %q/%q, deferrals %d; want one record, the deferred key reused, deferrals 1",
			len(f.records), f.evals[0].CheckKey, f.evals[1].CheckKey, f.evals[1].Deferrals)
	}
}

func TestEvaluationWorkflow_SignalsDuringAnEvaluationCoalesce(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: parkEvalAfter(2)}
	env := newEvalEnv(t, f)
	f.during = func(n int) {
		if n != 0 {
			return
		}

		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: []string{"b"}})
		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: []string{"a", "b"}})
	}

	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if in := f.evals[1]; in.All || !slices.Equal(in.Paths, []string{"a", "b"}) || in.Trigger != TriggerPush {
		t.Errorf("follow-up evaluation = %+v, want one push evaluation of the union [a b]", in)
	}
}

func TestCoalesce(t *testing.T) {
	t.Parallel()

	push := func(p Priority, paths ...string) *Recheck {
		return &Recheck{Trigger: TriggerPush, Priority: p, Paths: paths}
	}

	tests := []struct {
		name string
		in   []*Recheck
		want Recheck
	}{
		{
			name: "disjoint paths union",
			in:   []*Recheck{push(2, "b"), push(2, "a")},
			want: Recheck{Trigger: TriggerPush, Priority: 2, Paths: []string{"a", "b"}},
		},
		{
			name: "unknown dominates",
			in:   []*Recheck{push(2, "a"), {Trigger: TriggerPush, Priority: 2, Unknown: true}, push(2, "c")},
			want: Recheck{Trigger: TriggerPush, Priority: 2, Unknown: true},
		},
		{
			name: "highest priority wins",
			in:   []*Recheck{push(3, "a"), {Trigger: TriggerManual, Priority: PriorityManual, Paths: []string{"b"}}},
			want: Recheck{Trigger: TriggerManual, Priority: 1, Paths: []string{"a", "b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got *Recheck
			for _, s := range tt.in {
				got = coalesce(got, s)
			}

			same := got.Trigger == tt.want.Trigger && got.Priority == tt.want.Priority && got.Unknown == tt.want.Unknown
			if !same || !slices.Equal(got.Paths, tt.want.Paths) {
				t.Errorf("coalesce = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestEvaluationWorkflow_ContinuesAsNewAfterMaxIterations(t *testing.T) {
	t.Parallel()

	f := &evalFakes{}
	env := newEvalEnv(t, f)
	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	err := env.GetWorkflowError()

	var can *workflow.ContinueAsNewError
	if !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew after %d evaluations", err, maxIterationsPerRun)
	}

	if len(f.evals) != maxIterationsPerRun {
		t.Errorf("evaluations before ContinueAsNew = %d, want %d", len(f.evals), maxIterationsPerRun)
	}
}
