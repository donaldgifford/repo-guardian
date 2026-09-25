package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RouteWebhook's options (DESIGN-0026 § Activities).
const (
	routeWebhookTimeout     = 30 * time.Second
	routeWebhookMaxAttempts = 10
)

// WebhookWorkflow routes one GitHub delivery: a single RouteWebhook
// activity that upserts what it needs and signals the right workflows.
// Its ID, webhook/<delivery>, makes redeliveries idempotent.
func WebhookWorkflow(ctx workflow.Context, in *WebhookInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: routeWebhookTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    routeWebhookMaxAttempts,
		},
		Priority: TaskPriority(PriorityWebhook, in.InstallationID),
	})

	return workflow.ExecuteActivity(ctx, RouteWebhookActivity, in).Get(ctx, nil)
}
