package activities

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.temporal.io/sdk/activity"
	temporalmocks "go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// sweepStore answers DueRemediations and RemediationTarget by control.
type sweepStore struct {
	RemediationServiceStore // anything not overridden panics

	due     []store.DueRemediation
	targets map[string]*store.RemediationTarget
}

func (s *sweepStore) DueRemediations(context.Context, int) ([]store.DueRemediation, error) {
	return s.due, nil
}

func (s *sweepStore) RemediationTarget(_ context.Context, _ int64, c string) (*store.RemediationTarget, error) {
	return s.targets[c], nil
}

// TestSweep_StartsDueRemediationsAtRolloutPriority is IMPL-0030 6.1: a
// due control the Remediation App can act on is signal-with-started on
// the remediation queue at priority 4; one it cannot is left alone.
func TestSweep_StartsDueRemediationsAtRolloutPriority(t *testing.T) {
	t.Parallel()

	unreachable := remediable()
	unreachable.Remediable = false

	st := &sweepStore{
		due:     []store.DueRemediation{{RepositoryID: 9, ControlID: "codeowners"}, {RepositoryID: 9, ControlID: "notice"}},
		targets: map[string]*store.RemediationTarget{"codeowners": remediable(), "notice": unreachable},
	}
	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}

	s := NewRemediationServices(
		&RemediationServicesConfig{Store: st, Temporal: tc, RemediateQueue: "remediate", Logger: slog.New(slog.DiscardHandler)},
	)

	res, err := s.Sweep(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}

	if res.Due != 2 || res.Started != 1 {
		t.Errorf("Sweep = %+v, want 2 due and 1 started", res)
	}

	if len(tc.signals) != 1 || tc.signals[0].workflowID != workflows.RemediationWorkflowID(9, "codeowners") ||
		tc.signals[0].priority != int(workflows.PriorityRollout) || tc.signals[0].queue != "remediate" {
		t.Errorf("signals = %+v, want codeowners started at priority 4 on the remediation queue", tc.signals)
	}
}

// TestResolveAccess_HandsTheChangeToTheEvaluator is IMPL-0030 6.4: an
// access change becomes a Remediation App installation_repositories
// delivery on the evaluation queue, which RouteWebhook re-resolves.
func TestResolveAccess_HandsTheChangeToTheEvaluator(t *testing.T) {
	t.Parallel()

	tc := &recordingTemporal{Client: temporalmocks.NewClient(t)}
	s := NewRemediationServices(&RemediationServicesConfig{Temporal: tc, EvalQueue: "eval", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	var suite testsuite.WorkflowTestSuite

	env := suite.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(s.ResolveAccess, activity.RegisterOptions{Name: workflows.RemediationResolveAccessActivity})

	in := &workflows.ResolveAccessInput{InstallationID: 7, AccountLogin: "acme", Repos: []workflows.WebhookRepo{widgets}}
	if _, err := env.ExecuteActivity(workflows.RemediationResolveAccessActivity, in); err != nil {
		t.Fatal(err)
	}

	if len(tc.signals) != 1 {
		t.Fatalf("starts = %+v, want one", tc.signals)
	}

	got := tc.signals[0]
	wi, ok := got.arg.(*workflows.WebhookInput)

	if got.name != workflows.WebhookWorkflowName || got.queue != "eval" || !strings.HasPrefix(got.workflowID, "remediation-access/") || !ok ||
		wi.App != workflows.AppRemediate || !isAccessEvent(wi.Event) || len(wi.Repositories) != 1 || wi.InstallationID != 7 {
		t.Errorf("start = %+v (%+v), want a remediate access delivery on the evaluation queue", got, wi)
	}
}
