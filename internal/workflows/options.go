package workflows

import (
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Activity options per DESIGN-0026 § Activities.
const (
	checkRepoTimeout     = 15 * time.Minute
	checkRepoRetryFirst  = 30 * time.Second
	checkRepoRetryMax    = 30 * time.Minute
	checkRepoMaxAttempts = 10 // v1's MAX_JOB_ATTEMPTS default

	storeTimeout    = 30 * time.Second
	storeRetryFirst = time.Second
	storeRetryMax   = time.Minute

	// The evaluation's remediation hooks (IMPL-0030 2.6, 2.9) are best
	// effort: the sweep and the next evaluation catch what they miss, so
	// they never hold the loop.
	hookMaxAttempts = 5

	// A remediation attempt (IMPL-0030 3.3) heartbeats, so a lost worker
	// is noticed within remediateHeartbeat; StartToClose bounds one
	// attempt and ScheduleToClose every attempt together.
	remediateStartToClose    = 10 * time.Minute
	remediateScheduleToClose = 30 * time.Minute
	remediateHeartbeat       = time.Minute
	remediateRetryFirst      = 30 * time.Second
	remediateRetryMax        = 5 * time.Minute
)

// TaskPriority is the priority every repo-guardian workflow start and
// activity carries (DESIGN-0026 § Rate budget). The fairness key is the
// installation, so a 15,000-repository installation cannot starve a
// 50-repository one on the shared task queue; the priority key orders
// human-triggered checks ahead of scheduled ones.
func TaskPriority(p Priority, installationID int64) temporal.Priority {
	return temporal.Priority{PriorityKey: int(p), FairnessKey: strconv.FormatInt(installationID, 10)}
}

// checkRepoOptions runs Evaluate at priority p. A throttle is a Deferred
// result, not an error, so it never spends one of the attempts.
func checkRepoOptions(ctx workflow.Context, p temporal.Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: checkRepoTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    checkRepoRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    checkRepoRetryMax,
			MaximumAttempts:    checkRepoMaxAttempts,
		},
		Priority: p,
	})
}

// storeOptions runs the store activities (RecordEvaluation,
// ParkRepository) and AcquireBudget. They retry without limit: Postgres or the
// Temporal frontend being down is a wait, never a reason to lose a
// check's result.
func storeOptions(ctx workflow.Context, p temporal.Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: storeTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    storeRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    storeRetryMax,
			MaximumAttempts:    0, // unlimited
		},
		Priority: p,
	})
}

// hookOptions runs StartDueRemediations: bounded, because a failure is
// logged and caught by the next evaluation or the remediation sweep.
func hookOptions(ctx workflow.Context, p temporal.Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: storeTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    storeRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    storeRetryMax,
			MaximumAttempts:    hookMaxAttempts,
		},
		Priority: p,
	})
}

// localHookOptions runs SetNextDue as a local activity (OQ17): one row
// write, bounded like the other hooks.
func localHookOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: storeTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    storeRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    storeRetryMax,
			MaximumAttempts:    hookMaxAttempts,
		},
	})
}

// remediateOptions runs Remediate at priority p. A throttle, a hold, a
// busy operation and a stale epoch are results, not errors; the
// non-retryable types catch one that escapes as an error.
func remediateOptions(ctx workflow.Context, p temporal.Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    remediateStartToClose,
		ScheduleToCloseTimeout: remediateScheduleToClose,
		HeartbeatTimeout:       remediateHeartbeat,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        remediateRetryFirst,
			BackoffCoefficient:     2,
			MaximumInterval:        remediateRetryMax,
			NonRetryableErrorTypes: []string{ErrTypeOperationHeld, ErrTypeStaleEpoch, ErrTypeHold},
		},
		Priority: p,
	})
}
