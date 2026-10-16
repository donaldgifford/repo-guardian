package workflows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
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
		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: []string{"a"}})
	}

	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if in := f.evals[1]; in.All || !slices.Equal(in.Paths, []string{"a", "b"}) || in.Trigger != TriggerPush {
		t.Errorf("follow-up evaluation = %+v, want one push evaluation of the disjoint pushes' union [a b]", in)
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
			name: "union past the cap is unknown",
			in:   []*Recheck{push(2, maxPaths("a")...), push(2, "z")},
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

// maxPaths returns MaxChangedPaths distinct 60-byte paths under prefix,
// the largest change set a re-check carries (INV-0022 spike 5(c)).
func maxPaths(prefix string) []string {
	out := make([]string, 0, MaxChangedPaths)
	for i := range MaxChangedPaths {
		out = append(out, fmt.Sprintf("%s/%0*d", prefix, 59-len(prefix), i))
	}

	return out
}

func TestEvaluationWorkflow_UnknownSignalSelectsEveryControl(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: parkEvalAfter(2)}
	env := newEvalEnv(t, f)
	f.during = func(n int) {
		if n != 0 {
			return
		}

		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: []string{"a"}})
		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Unknown: true})
	}

	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if in := f.evals[1]; !in.All || in.Paths != nil || in.Scheduled {
		t.Errorf("follow-up evaluation = %+v, want every file control selected by the one unknown signal", in)
	}
}

func TestEvaluationWorkflow_ParkedRepositoryIsNeverReadAgain(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: func(int, *EvaluateInput) *EvaluateResult { return &EvaluateResult{Kind: CheckParked} }}
	env := newEvalEnv(t, f)
	f.during = func(int) {
		env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: []string{"a"}})
	}

	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}

	if len(f.evals) != 1 || len(f.records) != 0 || len(f.parks) != 0 {
		t.Errorf("evals %d, records %d, parks %d; want the loop ended after one evaluation found the repository parked",
			len(f.evals), len(f.records), len(f.parks))
	}
}

// TestEvaluationWorkflow_HistoryBoundedUnderMaxSignals sends the largest
// re-check the router emits during every evaluation of a full run, and
// two disjoint ones during the last: no evaluation carries more than
// MaxChangedPaths, and the ContinueAsNew state stays one signal's size.
// At about 16 KB of paths per iteration, 100 iterations stay near the
// 1.4 MB INV-0022 projected, under Temporal's 10 MB warning.
func TestEvaluationWorkflow_HistoryBoundedUnderMaxSignals(t *testing.T) {
	t.Parallel()

	f := &evalFakes{}
	env := newEvalEnv(t, f)

	var signalled int

	f.during = func(n int) {
		send := func(prefix string) {
			paths := maxPaths(prefix + strconv.Itoa(n))
			for _, p := range paths {
				signalled += len(p)
			}

			env.SignalWorkflow(RecheckSignal, &Recheck{Trigger: TriggerPush, Priority: PriorityWebhook, Paths: paths})
		}

		send("a")

		if n == maxIterationsPerRun-1 {
			send("b")
		}
	}

	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	var can *workflow.ContinueAsNewError
	if err := env.GetWorkflowError(); !errors.As(err, &can) {
		t.Fatalf("workflow error = %v, want ContinueAsNew", err)
	}

	for i, in := range f.evals {
		if len(in.Paths) > MaxChangedPaths {
			t.Fatalf("evaluation %d carried %d paths, want at most %d", i, len(in.Paths), MaxChangedPaths)
		}
	}

	carried := 0
	for _, p := range can.Input.GetPayloads() {
		carried += len(p.GetData())
	}

	if carried > 1024 {
		t.Errorf("ContinueAsNew state = %d bytes, want the two disjoint maximum signals coalesced to unknown, not their union", carried)
	}

	if perIteration := signalled / maxIterationsPerRun; perIteration > MaxChangedPaths*64 {
		t.Errorf("signal paths per iteration = %d bytes, want at most %d", perIteration, MaxChangedPaths*64)
	}
}
