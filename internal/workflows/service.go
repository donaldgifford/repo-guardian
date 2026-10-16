package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Service workflow sizing (IMPL-0025 Phase 13), shared by the controls
// service workflows.
const (
	// discoveryBatch is how many repositories one UpsertRepositories
	// activity upserts.
	discoveryBatch = 100
	// signalPage is how many repositories one SignalRepositories
	// activity signals.
	signalPage = 500

	serviceTimeout = 5 * time.Minute

	// detailSignalled is the service_runs detail key a rollout counts
	// its signals under.
	detailSignalled    = "signalled"
	listingMaxAttempts = 3
)

// serviceOptions runs a service activity. It retries without limit:
// every service activity is idempotent, and giving up would leave a
// rollout or a snapshot half done.
func serviceOptions(ctx workflow.Context, p Priority) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: serviceTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
		},
		Priority: TaskPriority(p, 0),
	})
}

// listingOptions runs a GitHub listing. It gives up after a few
// attempts: a failed listing is recorded as incomplete and parks
// nothing, and the next scheduled run tries again.
func listingOptions(ctx workflow.Context, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: serviceTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    listingMaxAttempts,
		},
		Priority: TaskPriority(PrioritySchedule, installationID),
	})
}

// chunk yields s in slices of at most n.
func chunk[T any](s []T, n int) func(yield func([]T) bool) {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			end := min(n, len(s))
			if !yield(s[:end]) {
				return
			}

			s = s[end:]
		}
	}
}

// recordRun writes the service_runs row.
func recordRun(ctx workflow.Context, kind string, success bool, started time.Time, detail map[string]any) error {
	run := &ServiceRun{Kind: kind, Success: success, StartedAt: started, FinishedAt: workflow.Now(ctx), Detail: detail}

	return workflow.ExecuteActivity(serviceOptions(ctx, PrioritySchedule), ControlsRecordServiceRunActivity, run).Get(ctx, nil)
}
