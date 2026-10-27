package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go.temporal.io/api/serviceerror"

	"github.com/donaldgifford/repo-guardian/internal/api"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

type recordingSignaler struct {
	id, name string
	arg      any
	err      error
}

func (s *recordingSignaler) SignalWorkflow(_ context.Context, id, _, name string, arg any) error {
	s.id, s.name, s.arg = id, name, arg

	return s.err
}

func TestEvaluationRechecker_SignalsAHumanRecheckOfEveryControl(t *testing.T) {
	t.Parallel()

	sig := &recordingSignaler{}
	if err := (evaluationRechecker{signaler: sig}).Recheck(t.Context(), 42); err != nil {
		t.Fatalf("Recheck(42) = %v, want nil", err)
	}

	want := workflows.Recheck{Trigger: workflows.TriggerManual, Priority: workflows.PriorityHuman, Unknown: true}
	got, ok := sig.arg.(workflows.Recheck)
	if sig.id != workflows.EvaluationWorkflowID(42) || sig.name != workflows.RecheckSignal || !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("signal = %s %s %+v, want %s %s %+v", sig.id, sig.name, sig.arg, workflows.EvaluationWorkflowID(42), workflows.RecheckSignal, want)
	}
}

func TestEvaluationRechecker_NotFoundIsWorkflowMissing(t *testing.T) {
	t.Parallel()

	sig := &recordingSignaler{err: serviceerror.NewNotFound("workflow not found")}
	if err := (evaluationRechecker{signaler: sig}).Recheck(t.Context(), 42); !errors.Is(err, api.ErrWorkflowMissing) {
		t.Errorf("Recheck on NotFound = %v, want api.ErrWorkflowMissing", err)
	}

	sig.err = errors.New("unavailable")
	if err := (evaluationRechecker{signaler: sig}).Recheck(t.Context(), 42); err == nil || errors.Is(err, api.ErrWorkflowMissing) {
		t.Errorf("Recheck on another error = %v, want it wrapped, not workflow_missing", err)
	}
}
