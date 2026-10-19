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

// TestRegister_EachRoleRegistersOnlyItsSet pins each controls worker's
// workflows (IMPL-0028 task 4.2, IMPL-0029 task 7.1): the evaluator runs
// the evaluation loop, its services and the webhook routing, the
// remediator its budget and the remediation loop (IMPL-0030 task 3.2),
// and the union registers each name once.
func TestRegister_EachRoleRegistersOnlyItsSet(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		register func(*recordingRegistry)
		want     []string
	}{
		{
			name:     "evaluator",
			register: func(r *recordingRegistry) { RegisterEvaluator(r) },
			want: []string{
				ControlsDiscoveryWorkflowName, ControlsRolloutWorkflowName, ControlsSnapshotWorkflowName,
				EvaluationWorkflowName, InstallationWorkflowName, WebhookWorkflowName,
			},
		},
		{
			name:     "remediator",
			register: func(r *recordingRegistry) { RegisterRemediator(r) },
			want:     []string{InstallationWorkflowName, RemediationWorkflowName},
		},
	} {
		var r recordingRegistry
		tt.register(&r)

		if got := slices.Sorted(slices.Values(r.names)); !slices.Equal(got, tt.want) {
			t.Errorf("%s registers %v, want %v", tt.name, got, tt.want)
		}
	}

	var union recordingRegistry
	RegisterUnion(&union)

	sorted := slices.Sorted(slices.Values(union.names))
	if len(slices.Compact(sorted)) != len(union.names) {
		t.Errorf("RegisterUnion registered %v, want each name once", union.names)
	}
}

// TestInstallationWorkflowID is IMPL-0028 task 4.6: each controls App
// has its own budget per installation, and the rc's id is unchanged.
func TestInstallationWorkflowID(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ app, want, holder string }{
		{"", "installation/7", "repo/1"},
		{AppEval, "installation/eval/7", "eval/repo/1"},
		{AppRemediate, "installation/remediate/7", "remediate/repo/1"},
	} {
		if got := InstallationWorkflowID(tt.app, 7); got != tt.want {
			t.Errorf("InstallationWorkflowID(%q, 7) = %q, want %q", tt.app, got, tt.want)
		}

		if got := BudgetHolder(tt.app, "repo/1"); got != tt.holder {
			t.Errorf("BudgetHolder(%q, repo/1) = %q, want %q", tt.app, got, tt.holder)
		}
	}
}
