package activities

import (
	"context"
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// observePRs reads the tracked pull requests through the Evaluation
// App's PRObserver (DESIGN-0032 D24): one paginated listing of the open
// repo-guardian/ PRs, then GetPullRequest for each tracked PR not in it.
// Matching is by number, so a PR someone else opened on a repo-guardian/
// branch is never tracked and never read. A failed page is an error, not
// an empty list: reading a short list as "closed" would reclassify open
// PRs.
func observePRs(ctx context.Context, o control.PRObserver, tracked []store.TrackedPR) ([]store.PRObservation, error) {
	if len(tracked) == 0 {
		return nil, nil
	}

	open, err := o.ListPullRequests(ctx, workflows.RemediationBranchPrefix)
	if err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}

	byNumber := make(map[int]*control.PullRequest, len(open))
	for i := range open {
		byNumber[open[i].Number] = &open[i]
	}

	out := make([]store.PRObservation, 0, len(tracked))

	for _, t := range tracked {
		pr, ok := byNumber[t.PRNumber]
		if !ok {
			if pr, err = o.GetPullRequest(ctx, t.PRNumber); err != nil {
				return nil, fmt.Errorf("get pull request #%d: %w", t.PRNumber, err)
			}
		}

		out = append(out, observation(t, pr))
	}

	return out, nil
}

// prStateClosed is GitHub's state for a closed (merged or not) PR.
const prStateClosed = "closed"

func observation(t store.TrackedPR, pr *control.PullRequest) store.PRObservation {
	obs := store.PRObservation{RemediationID: t.RemediationID, PRNumber: t.PRNumber, State: store.PRObservedOpen, HeadSHA: pr.HeadSHA}

	switch {
	case pr.Merged:
		obs.State, obs.ClosedAt = store.PRObservedMerged, pr.ClosedAt
	case pr.State == prStateClosed:
		obs.State, obs.ClosedAt = store.PRObservedClosed, pr.ClosedAt
	}

	return obs
}
