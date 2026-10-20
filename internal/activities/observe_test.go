package activities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// fakeObserver answers ListPullRequests from open and GetPullRequest
// from closed, and records what was asked.
type fakeObserver struct {
	control.PRObserver

	open    []control.PullRequest
	closed  map[int]*control.PullRequest
	listErr error
	prefix  string
	gets    []int
}

func (f *fakeObserver) ListPullRequests(_ context.Context, prefix string) ([]control.PullRequest, error) {
	f.prefix = prefix

	return f.open, f.listErr
}

func (f *fakeObserver) GetPullRequest(_ context.Context, n int) (*control.PullRequest, error) {
	f.gets = append(f.gets, n)

	if pr, ok := f.closed[n]; ok {
		return pr, nil
	}

	return nil, errors.New("not found")
}

// TestObservePRs is IMPL-0030 2.7's observation table: an open PR with a
// new head, a merge, a user close, a row the remediator closed (never
// tracked, so never read), a failed page, and someone else's PR on a
// repo-guardian/ branch (ignored: matching is by number).
func TestObservePRs(t *testing.T) {
	t.Parallel()

	closedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tracked := []store.TrackedPR{
		{RemediationID: 1, ControlID: "codeowners", PRNumber: 7},
		{RemediationID: 2, ControlID: "labels", PRNumber: 8},
		{RemediationID: 3, ControlID: "catalog-info", PRNumber: 9},
	}
	obs := &fakeObserver{
		open: []control.PullRequest{
			{Number: 7, State: "open", HeadSHA: "h2", HeadRef: "repo-guardian/codeowners"},
			{Number: 99, State: "open", HeadSHA: "x", HeadRef: "repo-guardian/someone-else"},
		},
		closed: map[int]*control.PullRequest{
			8: {Number: 8, State: "closed", Merged: true, ClosedAt: closedAt},
			9: {Number: 9, State: "closed", ClosedAt: closedAt},
		},
	}

	got, err := observePRs(t.Context(), obs, tracked)
	if err != nil {
		t.Fatal(err)
	}

	want := []store.PRObservation{
		{RemediationID: 1, PRNumber: 7, State: store.PRObservedOpen, HeadSHA: "h2"},
		{RemediationID: 2, PRNumber: 8, State: store.PRObservedMerged, ClosedAt: closedAt},
		{RemediationID: 3, PRNumber: 9, State: store.PRObservedClosed, ClosedAt: closedAt},
	}

	if len(got) != len(want) {
		t.Fatalf("observePRs = %+v, want %+v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("observation %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if obs.prefix != workflows.RemediationBranchPrefix || len(obs.gets) != 2 {
		t.Errorf("listed %q and read %v; want the remediation prefix and only #8 and #9", obs.prefix, obs.gets)
	}

	t.Run("nothing tracked reads nothing", func(t *testing.T) {
		t.Parallel()

		quiet := &fakeObserver{}
		if got, err := observePRs(t.Context(), quiet, nil); err != nil || got != nil || quiet.prefix != "" {
			t.Errorf("observePRs(nothing tracked) = %v, %v, listed %q; want no call", got, err, quiet.prefix)
		}
	})

	t.Run("a failed page is an error, not an empty list", func(t *testing.T) {
		t.Parallel()

		failing := &fakeObserver{listErr: errors.New("page 2: 502")}
		if got, err := observePRs(t.Context(), failing, tracked); err == nil || got != nil {
			t.Errorf("observePRs(failed page) = %v, %v; want an error and no observations", got, err)
		}
	})
}

// TestEvaluate_ObserveOnly is IMPL-0030 2.3 at the activity: a pr_event
// run records observations, stages nothing and reports CheckObserved.
func TestEvaluate_ObserveOnly(t *testing.T) {
	t.Parallel()

	f := newEvalFixture(t)
	f.st.tracked = []store.TrackedPR{{RemediationID: 1, ControlID: "codeowners", PRNumber: 7}}
	f.repo.observer = &fakeObserver{open: []control.PullRequest{{Number: 7, State: "open", HeadSHA: "h1"}}}

	res, err := f.e.Evaluate(
		t.Context(),
		&workflows.EvaluateInput{RepositoryID: testRepoID, CheckKey: "k", Trigger: workflows.TriggerPREvent, Observe: true},
	)
	if err != nil {
		t.Fatal(err)
	}

	if res.Kind != workflows.CheckObserved || f.st.staged != nil || len(f.st.observed) != 1 || f.st.observed[0].HeadSHA != "h1" {
		t.Errorf("Evaluate(observe) = %+v, staged %v, observed %+v; want observed only", res, f.st.staged, f.st.observed)
	}
}
