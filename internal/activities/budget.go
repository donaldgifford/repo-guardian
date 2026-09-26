package activities

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// Budget is the AcquireBudget activity. Workflow code cannot call
// Update-with-Start, so RepoWorkflow reaches its InstallationWorkflow
// through this activity.
type Budget struct {
	client    client.Client
	taskQueue string
	threshold float64
	leaseTTL  time.Duration

	// MaxHandled is passed to new InstallationWorkflows; 0 keeps the
	// workflow default. The burst test sets it.
	MaxHandled int
}

// NewBudget returns the budget activity. threshold is
// RATE_LIMIT_THRESHOLD, the reserve each InstallationWorkflow keeps.
func NewBudget(c client.Client, taskQueue string, threshold float64) *Budget {
	return &Budget{client: c, taskQueue: taskQueue, threshold: threshold, leaseTTL: workflows.DefaultLeaseTTL}
}

// AcquireBudget sends acquire to installation/<id>, starting the
// InstallationWorkflow if it is not running.
func (b *Budget) AcquireBudget(ctx context.Context, in *workflows.AcquireInput) (*workflows.AcquireResult, error) {
	id := workflows.InstallationWorkflowID(in.InstallationID)

	start := b.client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       id,
		TaskQueue:                b.taskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		Priority:                 workflows.TaskPriority(in.Request.Priority, in.InstallationID),
	}, workflows.InstallationWorkflowName, &workflows.InstallationWorkflowInput{
		InstallationID: in.InstallationID,
		Threshold:      b.threshold,
		LeaseTTL:       b.leaseTTL,
		MaxHandled:     b.MaxHandled,
	})

	handle, err := b.client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     in.UpdateID,
			UpdateName:   workflows.AcquireUpdate,
			Args:         []any{&in.Request},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", id, err)
	}

	var res workflows.AcquireResult
	if err := handle.Get(ctx, &res); err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", id, err)
	}

	metrics.BudgetAcquireTotal.WithLabelValues(acquireResult(&res)).Inc()

	return &res, nil
}

// acquireResult is the budget_acquire_total result label.
func acquireResult(r *workflows.AcquireResult) string {
	switch {
	case !r.Granted:
		return "wait"
	case r.Optimistic:
		return "optimistic"
	default:
		return "granted"
	}
}

// Register registers AcquireBudget under its workflows package name.
func (b *Budget) Register(r Registry) {
	r.RegisterActivityWithOptions(b.AcquireBudget, activity.RegisterOptions{Name: workflows.AcquireBudgetActivity})
}
