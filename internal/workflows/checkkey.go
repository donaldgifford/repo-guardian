package workflows

import (
	"strconv"

	"go.temporal.io/sdk/workflow"
)

// checkKey is the idempotency key for one check:
// <workflowID>/<runID>/<iteration>. It is deterministic, so a replayed
// or retried activity reuses it, and RecordCheck's key uniqueness turns
// the retry into a no-op. Deferred retries of the same iteration reuse
// it too, which is safe because a deferred check stages nothing.
func checkKey(ctx workflow.Context, iteration int) string {
	exec := workflow.GetInfo(ctx).WorkflowExecution

	return exec.ID + "/" + exec.RunID + "/" + strconv.Itoa(iteration)
}
