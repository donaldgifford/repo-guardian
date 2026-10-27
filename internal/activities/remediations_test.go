package activities

import (
	"context"
	"testing"

	temporalmocks "go.temporal.io/sdk/mocks"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// TestStartRemediations is IMPL-0028 task 4.7: one signal-with-start per
// due control, on the remediation queue, at the entry's priority.
func TestStartRemediations(t *testing.T) {
	t.Parallel()

	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}
	s := NewRemediationStarter(tc, "repo-guardian-remediate", nil)

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

type dueFake []store.DueRemediation

func (d dueFake) DueRemediationsFor(context.Context, int64) ([]store.DueRemediation, error) {
	return d, nil
}

// TestStartDueRemediations is IMPL-0030 2.5: every due control is
// signal-with-started once at priority 3 on the remediation queue, and
// nothing due starts nothing.
func TestStartDueRemediations(t *testing.T) {
	t.Parallel()

	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}
	s := NewRemediationStarter(
		tc,
		"repo-guardian-remediate",
		dueFake{{RepositoryID: 9, ControlID: "codeowners"}, {RepositoryID: 9, ControlID: "labels"}},
	)

	res, err := s.StartDueRemediations(t.Context(), &workflows.StartDueRemediationsInput{RepositoryID: 9, InstallationID: 7})
	if err != nil || res.Started != 2 || len(tc.signals) != 2 {
		t.Fatalf("StartDueRemediations = %+v, %v, %d signals; want 2 started", res, err, len(tc.signals))
	}

	for i, id := range []string{"remediation/9/codeowners", "remediation/9/labels"} {
		got := tc.signals[i]
		if got.workflowID != id || got.name != workflows.RecheckSignal || got.priority != 3 || got.queue != "repo-guardian-remediate" ||
			!got.started {
			t.Errorf("signal %d = %+v, want a priority-3 recheck signal-with-start of %s", i, got, id)
		}
	}

	none := NewRemediationStarter(tc, "repo-guardian-remediate", dueFake(nil))
	if res, err := none.StartDueRemediations(t.Context(), &workflows.StartDueRemediationsInput{RepositoryID: 9}); err != nil || res.Started != 0 ||
		len(tc.signals) != 2 {
		t.Errorf("nothing due = %+v, %v, %d signals; want nothing started", res, err, len(tc.signals))
	}
}
