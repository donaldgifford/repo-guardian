package activities

import (
	"testing"

	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestStartRemediations is IMPL-0028 task 4.7: one signal-with-start per
// due control, on the remediation queue, at the entry's priority.
func TestStartRemediations(t *testing.T) {
	t.Parallel()

	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}
	s := NewRemediationStarter(tc, "repo-guardian-remediate")

	err := s.StartRemediations(t.Context(), []workflows.RemediationStart{
		{RepositoryID: 9, Control: "codeowners", InstallationID: 7, Priority: workflows.PrioritySchedule},
		{RepositoryID: 9, Control: "renovate", InstallationID: 7, Priority: workflows.PriorityHuman},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []signal{
		{workflowID: "remediation/9/codeowners", name: workflows.RemediateSignal, started: true, priority: 3, queue: "repo-guardian-remediate"},
		{workflowID: "remediation/9/renovate", name: workflows.RemediateSignal, started: true, priority: 1, queue: "repo-guardian-remediate"},
	}

	if len(tc.signals) != len(want) {
		t.Fatalf("signals = %+v, want %+v", tc.signals, want)
	}

	for i := range want {
		got := tc.signals[i]
		if got.workflowID != want[i].workflowID || got.name != want[i].name || got.priority != want[i].priority || got.queue != want[i].queue {
			t.Errorf("signal %d = %+v, want %+v", i, got, want[i])
		}
	}
}
