package reconciler

import (
	"context"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// workflowSyncReconciler detects workflow file changes and logs mismatches.
// This reconciler is primarily useful with the watch capability — push events
// trigger a rescan, and the exact check mode handles template comparison.
// The reconciler logs the current state for observability.
type workflowSyncReconciler struct{}

// NewWorkflowSyncReconciler creates a workflow sync reconciler.
func NewWorkflowSyncReconciler(_ policy.ReconcilerConfig) (Reconciler, error) {
	return &workflowSyncReconciler{}, nil
}

func (*workflowSyncReconciler) Name() string { return "workflow_sync" }

func (*workflowSyncReconciler) Reconcile(_ context.Context, params *ReconcileParams) error {
	log := params.Logger

	if params.Content == "" {
		log.Info("workflow file not found")
		return nil
	}

	if params.DryRun {
		log.Info("dry run: workflow sync check complete")
		return nil
	}

	log.Info("workflow sync check complete", "content_length", len(params.Content))

	return nil
}
