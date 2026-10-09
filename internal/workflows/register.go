package workflows

import (
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// registration is one workflow function under its registered name.
type registration struct {
	fn   any
	name string
}

// rcSet is the rc worker's workflows: every v2 workflow on the rc's one
// task queue.
var rcSet = []registration{
	{RepoWorkflow, RepoWorkflowName},
	{InstallationWorkflow, InstallationWorkflowName},
	{WebhookWorkflow, WebhookWorkflowName},
	{DiscoveryWorkflow, DiscoveryWorkflowName},
	{SnapshotWorkflow, SnapshotWorkflowName},
	{PolicyRolloutWorkflow, PolicyRolloutWorkflowName},
	{BootstrapWorkflow, BootstrapWorkflowName},
}

// evaluatorSet and remediatorSet are the controls workers' workflows,
// one set per task queue (IMPL-0028 task 4.2). Each App keeps its own
// rate budget, so both run InstallationWorkflow; IMPL-0029 and
// IMPL-0030 add the evaluation and remediation workflows.
var (
	evaluatorSet  = []registration{{InstallationWorkflow, InstallationWorkflowName}}
	remediatorSet = []registration{{InstallationWorkflow, InstallationWorkflowName}}
)

// Register registers the rc worker's workflows.
func Register(r worker.WorkflowRegistry) { register(r, rcSet) }

// RegisterEvaluator registers the evaluator role's workflows.
func RegisterEvaluator(r worker.WorkflowRegistry) { register(r, evaluatorSet) }

// RegisterRemediator registers the remediator role's workflows.
func RegisterRemediator(r worker.WorkflowRegistry) { register(r, remediatorSet) }

// RegisterUnion registers every workflow any role runs, once each: what
// a replayer needs to replay a history from any queue.
func RegisterUnion(r worker.WorkflowRegistry) {
	seen := make(map[string]bool)

	for _, set := range [][]registration{rcSet, evaluatorSet, remediatorSet} {
		for _, reg := range set {
			if !seen[reg.name] {
				seen[reg.name] = true
				r.RegisterWorkflowWithOptions(reg.fn, workflow.RegisterOptions{Name: reg.name})
			}
		}
	}
}

func register(r worker.WorkflowRegistry, set []registration) {
	for _, reg := range set {
		r.RegisterWorkflowWithOptions(reg.fn, workflow.RegisterOptions{Name: reg.name})
	}
}
