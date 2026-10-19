package activities

import (
	"log/slog"
	"slices"
	"testing"

	"go.temporal.io/sdk/activity"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// nameRegistry records registered activity names.
type nameRegistry struct{ names []string }

func (r *nameRegistry) RegisterActivityWithOptions(_ any, o activity.RegisterOptions) {
	r.names = append(r.names, o.Name)
}

// TestRegister_RemediationActivitiesOnlyOnTheRemediator is IMPL-0030
// task 3.2: the Remediator registers Remediate and ReadRemediationState,
// and none of the evaluator worker's registrars does.
func TestRegister_RemediationActivitiesOnlyOnTheRemediator(t *testing.T) {
	t.Parallel()

	remediation := []string{workflows.ReadRemediationStateActivity, workflows.RemediateActivity}

	var rem nameRegistry
	NewRemediator(nil, nil, nil, ghclient.BotIdentity{}, slog.New(slog.DiscardHandler)).Register(&rem)

	if got := slices.Sorted(slices.Values(rem.names)); !slices.Equal(got, remediation) {
		t.Errorf("Remediator registers %v, want %v", got, remediation)
	}

	var eval nameRegistry
	logger := slog.New(slog.DiscardHandler)
	NewEvaluator(nil, nil, nil, logger).Register(&eval)
	NewControlsServices(&ControlsServicesConfig{Logger: logger}).Register(&eval)
	NewRouter(logger).Register(&eval)
	NewRemediationStarter(nil, "", nil).Register(&eval)
	NewBudget(nil, workflows.AppEval, "", 0).Register(&eval)

	for _, name := range remediation {
		if slices.Contains(eval.names, name) {
			t.Errorf("the evaluator's registrars register %s; only the remediator may", name)
		}
	}
}
