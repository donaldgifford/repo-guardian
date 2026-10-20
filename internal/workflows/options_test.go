package workflows

import (
	"slices"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// optionsProbe returns the activity options each helper sets.
func optionsProbe(ctx workflow.Context) ([2]workflow.ActivityOptions, error) {
	return [2]workflow.ActivityOptions{
		workflow.GetActivityOptions(checkRepoOptions(ctx, TaskPriority(PriorityWebhook, 7))),
		workflow.GetActivityOptions(storeOptions(ctx, TaskPriority(PrioritySchedule, 7))),
	}, nil
}

func TestActivityOptions_MatchTheDesignTable(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(optionsProbe)

	var got [2]workflow.ActivityOptions
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("probe: %v", err)
	}

	check, st := got[0], got[1]

	if check.StartToCloseTimeout != 15*time.Minute || check.HeartbeatTimeout != 0 {
		t.Errorf("CheckRepo timeouts = %v/%v, want 15m, no heartbeat", check.StartToCloseTimeout, check.HeartbeatTimeout)
	}

	if rp := check.RetryPolicy; rp == nil || rp.InitialInterval != 30*time.Second || rp.MaximumInterval != 30*time.Minute ||
		rp.MaximumAttempts != 10 || rp.BackoffCoefficient != 2 {
		t.Errorf("CheckRepo retry = %+v, want 30s→30m ×2, 10 attempts", check.RetryPolicy)
	}

	if check.Priority.PriorityKey != int(PriorityWebhook) || check.Priority.FairnessKey != "7" {
		t.Errorf("CheckRepo priority = %+v, want key %d, fairness 7", check.Priority, PriorityWebhook)
	}

	if st.Priority.PriorityKey != int(PrioritySchedule) || st.Priority.FairnessKey != "7" {
		t.Errorf("store priority = %+v, want key %d, fairness 7", st.Priority, PrioritySchedule)
	}

	if st.StartToCloseTimeout != 30*time.Second {
		t.Errorf("store timeout = %v, want 30s", st.StartToCloseTimeout)
	}

	if rp := st.RetryPolicy; rp == nil || rp.InitialInterval != time.Second || rp.MaximumInterval != time.Minute || rp.MaximumAttempts != 0 {
		t.Errorf("store retry = %+v, want 1s→1m, unlimited", st.RetryPolicy)
	}
}

func remediateOptionsProbe(ctx workflow.Context) (workflow.ActivityOptions, error) {
	return workflow.GetActivityOptions(remediateOptions(ctx, TaskPriority(PrioritySchedule, 7))), nil
}

// TestRemediateOptions is IMPL-0030 3.3 and 3.4: Remediate heartbeats,
// StartToClose sits below ScheduleToClose, the fence and hold outcomes
// are never retried, and the Remediation App's lease outlives every
// attempt.
func TestRemediateOptions(t *testing.T) {
	t.Parallel()

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(remediateOptionsProbe)

	var got workflow.ActivityOptions
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("probe: %v", err)
	}

	if got.HeartbeatTimeout == 0 || got.ScheduleToCloseTimeout == 0 || got.StartToCloseTimeout >= got.ScheduleToCloseTimeout {
		t.Errorf("Remediate timeouts = heartbeat %v, start %v, schedule %v; want a heartbeat and start below schedule",
			got.HeartbeatTimeout, got.StartToCloseTimeout, got.ScheduleToCloseTimeout)
	}

	want := []string{ErrTypeOperationHeld, ErrTypeStaleEpoch, ErrTypeHold}
	if rp := got.RetryPolicy; rp == nil || !slices.Equal(rp.NonRetryableErrorTypes, want) {
		t.Errorf("Remediate retry = %+v, want non-retryable %v", got.RetryPolicy, want)
	}

	if ttl := LeaseTTL(AppRemediate); ttl <= got.ScheduleToCloseTimeout {
		t.Errorf("LeaseTTL(remediate) = %v, want past ScheduleToClose %v", ttl, got.ScheduleToCloseTimeout)
	}

	if ttl := LeaseTTL(AppEval); ttl != DefaultLeaseTTL {
		t.Errorf("LeaseTTL(eval) = %v, want DefaultLeaseTTL", ttl)
	}
}
