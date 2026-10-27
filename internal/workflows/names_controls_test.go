package workflows

import (
	"testing"
	"time"
)

func TestControlsWorkflowIDs(t *testing.T) {
	t.Parallel()

	activated := time.Unix(1_760_000_000, 0)

	tests := []struct{ got, want string }{
		{EvaluationWorkflowID(42), "evaluation/42"},
		{ControlsDiscoveryWorkflowID(7, "d-1"), "controls-discovery/installation/7/d-1"},
		{ControlsRolloutWorkflowID("controls:abc", activated), "controls-rollout/controls:abc/1760000000"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("id = %q, want %q", tt.got, tt.want)
		}
	}

	// A revert to a version seen before is a new activation, so a new id.
	if ControlsRolloutWorkflowID("v", activated) == ControlsRolloutWorkflowID("v", activated.Add(time.Second)) {
		t.Error("ControlsRolloutWorkflowID ignores activated_at, want one rollout per activation")
	}
}
