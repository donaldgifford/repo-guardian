package workflows

import (
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
)

// checkRepoOptions runs CheckRepo at priority p. A throttle is a
// Deferred result, not an error, so it never spends one of the attempts.
func checkRepoOptions(ctx workflow.Context, p Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: checkRepoTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    checkRepoRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    checkRepoRetryMax,
			MaximumAttempts:    checkRepoMaxAttempts,
		},
		Priority: temporal.Priority{PriorityKey: int(p)},
	})
}

// storeOptions runs the store activities (RecordCheck, RecordCheckError,
// Park) and AcquireBudget. They retry without limit: Postgres or the
// Temporal frontend being down is a wait, never a reason to lose a
// check's result.
func storeOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: storeTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    storeRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    storeRetryMax,
			MaximumAttempts:    0, // unlimited
		},
	})
}
