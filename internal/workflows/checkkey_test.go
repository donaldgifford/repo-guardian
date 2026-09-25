package workflows

import (
	"strconv"
	"strings"
	"testing"
)

func TestCheckKey_WorkflowRunIteration(t *testing.T) {
	t.Parallel()

	fakes := &fakeActivities{script: parkAfter(2)}
	env := newEnv(t, fakes)
	env.ExecuteWorkflow(RepoWorkflowName, input())

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if len(fakes.checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(fakes.checks))
	}

	var runID string

	for i, c := range fakes.checks {
		parts := strings.Split(c.CheckKey, "/")
		if len(parts) != 4 || parts[0] != "repo" || parts[1] != "42" || parts[2] == "" || parts[3] != strconv.Itoa(i) {
			t.Fatalf("check %d key = %q, want repo/42/<run>/%d", i, c.CheckKey, i)
		}

		if runID == "" {
			runID = parts[2]
		} else if parts[2] != runID {
			t.Errorf("check %d run id = %q, want %q", i, parts[2], runID)
		}
	}

	// Record carries the key CheckRepo staged under.
	for i, rec := range fakes.records {
		if rec.Result.CheckKey != fakes.checks[i].CheckKey {
			t.Errorf("record %d key = %q, want %q", i, rec.Result.CheckKey, fakes.checks[i].CheckKey)
		}
	}
}
