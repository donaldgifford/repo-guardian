package control

import (
	"fmt"
	"testing"
)

func TestEvaluationPermissions_ReadOnly(t *testing.T) {
	t.Parallel()

	for _, p := range EvaluationPermissions() {
		if p.Access != AccessRead {
			t.Errorf("Evaluation App permission %s, want read only", p)
		}
	}
}

func TestRemediationPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		resources     []string
		workflowApply bool
		want          string
	}{
		{
			name: "base",
			want: "[Contents: write Metadata: read Pull requests: write]",
		},
		{
			name:      "settings and labels",
			resources: []string{ResourceAdministration, ResourceIssues, ResourceAdministration},
			want:      "[Administration: write Contents: write Issues: write Metadata: read Pull requests: write]",
		},
		{
			name:          "workflow apply adds Workflows",
			workflowApply: true,
			want:          "[Contents: write Metadata: read Pull requests: write Workflows: write]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprint(RemediationPermissions(tt.resources, tt.workflowApply)); got != tt.want {
				t.Errorf("RemediationPermissions(%v, %v) = %s, want %s", tt.resources, tt.workflowApply, got, tt.want)
			}
		})
	}
}
