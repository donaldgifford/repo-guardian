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

// evaluatorSet and remediatorSet are the controls workers' workflows,
// one set per task queue (IMPL-0028 task 4.2). Each App keeps its own
// rate budget, so both run InstallationWorkflow. The evaluator also
// routes both Apps' webhook deliveries and runs the evaluation loop and
// its service workflows (IMPL-0029 task 7.1); the remediator runs the
// remediation loop (IMPL-0030 task 3.2) and its sweep and discovery
// schedules (Phase 6), which nothing else registers.
var (
	evaluatorSet = []registration{
		{InstallationWorkflow, InstallationWorkflowName},
		{WebhookWorkflow, WebhookWorkflowName},
		{EvaluationWorkflow, EvaluationWorkflowName},
		{ControlsDiscoveryWorkflow, ControlsDiscoveryWorkflowName},
		{ControlsRolloutWorkflow, ControlsRolloutWorkflowName},
		{ControlsSnapshotWorkflow, ControlsSnapshotWorkflowName},
	}
	remediatorSet = []registration{
		{InstallationWorkflow, InstallationWorkflowName},
		{RemediationWorkflow, RemediationWorkflowName},
		{RemediationSweepWorkflow, RemediationSweepWorkflowName},
		{RemediationDiscoveryWorkflow, RemediationDiscoveryWorkflowName},
	}
)

// RegisterEvaluator registers the evaluator role's workflows.
func RegisterEvaluator(r worker.WorkflowRegistry) { register(r, evaluatorSet) }

// RegisterRemediator registers the remediator role's workflows.
func RegisterRemediator(r worker.WorkflowRegistry) { register(r, remediatorSet) }

// RegisterUnion registers every workflow any role runs, once each: what
// a replayer needs to replay a history from any queue.
func RegisterUnion(r worker.WorkflowRegistry) {
	seen := make(map[string]bool)

	for _, set := range [][]registration{evaluatorSet, remediatorSet} {
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
