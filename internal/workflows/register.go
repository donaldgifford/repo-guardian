package workflows

import (
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Register registers every workflow under its name.
func Register(r worker.WorkflowRegistry) {
	r.RegisterWorkflowWithOptions(RepoWorkflow, workflow.RegisterOptions{Name: RepoWorkflowName})
}
