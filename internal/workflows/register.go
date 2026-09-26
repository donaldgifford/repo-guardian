package workflows

import (
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Register registers every workflow under its name.
func Register(r worker.WorkflowRegistry) {
	r.RegisterWorkflowWithOptions(RepoWorkflow, workflow.RegisterOptions{Name: RepoWorkflowName})
	r.RegisterWorkflowWithOptions(InstallationWorkflow, workflow.RegisterOptions{Name: InstallationWorkflowName})
	r.RegisterWorkflowWithOptions(WebhookWorkflow, workflow.RegisterOptions{Name: WebhookWorkflowName})
	r.RegisterWorkflowWithOptions(DiscoveryWorkflow, workflow.RegisterOptions{Name: DiscoveryWorkflowName})
	r.RegisterWorkflowWithOptions(SnapshotWorkflow, workflow.RegisterOptions{Name: SnapshotWorkflowName})
	r.RegisterWorkflowWithOptions(PolicyRolloutWorkflow, workflow.RegisterOptions{Name: PolicyRolloutWorkflowName})
	r.RegisterWorkflowWithOptions(BootstrapWorkflow, workflow.RegisterOptions{Name: BootstrapWorkflowName})
}
