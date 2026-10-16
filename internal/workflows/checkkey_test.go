package workflows

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testRepoID   int64 = 42
	testInterval       = 24 * time.Hour
)

func TestCheckKey_WorkflowRunIteration(t *testing.T) {
	t.Parallel()

	f := &evalFakes{script: parkEvalAfter(1)}
	env := newEvalEnv(t, f)
	env.ExecuteWorkflow(EvaluationWorkflowName, evalInput())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(f.evals) != 2 {
		t.Fatalf("evaluations = %d, want 2", len(f.evals))
	}

	var runID string

	for i, e := range f.evals {
		parts := strings.Split(e.CheckKey, "/")
		if len(parts) != 4 || parts[0] != "evaluation" || parts[1] != "42" || parts[2] == "" || parts[3] != strconv.Itoa(i) {
			t.Fatalf("evaluation %d key = %q, want evaluation/42/<run>/%d", i, e.CheckKey, i)
		}

		if runID == "" {
			runID = parts[2]
		} else if parts[2] != runID {
			t.Errorf("evaluation %d run id = %q, want %q", i, parts[2], runID)
		}
	}
}
