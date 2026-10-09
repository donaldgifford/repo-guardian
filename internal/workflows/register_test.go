package workflows

import (
	"slices"
	"testing"

	"go.temporal.io/sdk/workflow"
)

// recordingRegistry records registered workflow names.
type recordingRegistry struct{ names []string }

func (*recordingRegistry) RegisterWorkflow(any) {}

func (r *recordingRegistry) RegisterWorkflowWithOptions(_ any, o workflow.RegisterOptions) {
	r.names = append(r.names, o.Name)
}

func (*recordingRegistry) RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions) {}

// TestRegister_EachRoleRegistersOnlyItsSet is IMPL-0028 task 4.2: the
// controls workers never register the rc's workflows, and the union
// registers each name once.
func TestRegister_EachRoleRegistersOnlyItsSet(t *testing.T) {
	t.Parallel()

	for name, register := range map[string]func(*recordingRegistry){
		"evaluator":  func(r *recordingRegistry) { RegisterEvaluator(r) },
		"remediator": func(r *recordingRegistry) { RegisterRemediator(r) },
	} {
		var r recordingRegistry
		register(&r)

		if slices.Contains(r.names, RepoWorkflowName) || slices.Contains(r.names, WebhookWorkflowName) {
			t.Errorf("%s registers %v, want no rc workflow", name, r.names)
		}
	}

	var union recordingRegistry
	RegisterUnion(&union)

	sorted := slices.Sorted(slices.Values(union.names))
	if len(slices.Compact(sorted)) != len(union.names) {
		t.Errorf("RegisterUnion registered %v, want each name once", union.names)
	}
}
