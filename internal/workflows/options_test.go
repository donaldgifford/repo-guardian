package workflows

import (
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// optionsProbe returns the activity options each helper sets.
func optionsProbe(ctx workflow.Context) ([2]workflow.ActivityOptions, error) {
	return [2]workflow.ActivityOptions{
		workflow.GetActivityOptions(checkRepoOptions(ctx, PriorityWebhook)),
		workflow.GetActivityOptions(storeOptions(ctx)),
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

	if check.Priority.PriorityKey != int(PriorityWebhook) {
		t.Errorf("CheckRepo priority = %d, want %d", check.Priority.PriorityKey, PriorityWebhook)
	}

	if st.StartToCloseTimeout != 30*time.Second {
		t.Errorf("store timeout = %v, want 30s", st.StartToCloseTimeout)
	}

	if rp := st.RetryPolicy; rp == nil || rp.InitialInterval != time.Second || rp.MaximumInterval != time.Minute || rp.MaximumAttempts != 0 {
		t.Errorf("store retry = %+v, want 1s→1m, unlimited", st.RetryPolicy)
	}
}
