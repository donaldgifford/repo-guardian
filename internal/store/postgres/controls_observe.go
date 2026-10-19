package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// remediationEventObserved is the remediation_events kind an observation
// writes.
const remediationEventObserved = "observed"

// TrackedPullRequests is the repository's open remediation pull
// requests, the evaluator's observation set.
func (s *ControlsStore) TrackedPullRequests(ctx context.Context, repoID int64) ([]store.TrackedPR, error) {
	rows, err := s.q.TrackedPullRequests(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("tracked pull requests: %w", err)
	}

	out := make([]store.TrackedPR, 0, len(rows))
	for _, r := range rows {
		out = append(out, store.TrackedPR{RemediationID: r.ID, ControlID: r.ControlID, PRNumber: int(r.PrNumber)})
	}

	return out, nil
}

// ObservePullRequests writes what the Evaluation App saw, as
// rg_evaluator and only through the columns it holds (DESIGN-0032 D24):
// the observed head of an open PR, or merged / closed_by_user with
// closed_at, closed_generation and next_eligible_at. Every write is
// guarded on state = 'open', so a row the remediator already closed is
// never reclassified. A remediation event records each state change and
// each moved head; an unchanged open PR only refreshes observed_at.
func (s *ControlsStore) ObservePullRequests(ctx context.Context, obs []store.PRObservation) error {
	if len(obs) == 0 {
		return nil
	}

	return s.inTx(ctx, func(q *controlsdb.Queries) error {
		for i := range obs {
			if err := observe(ctx, q, &obs[i]); err != nil {
				return fmt.Errorf("observe #%d: %w", obs[i].PRNumber, err)
			}
		}

		return nil
	})
}

func observe(ctx context.Context, q *controlsdb.Queries, o *store.PRObservation) error {
	var (
		changed bool
		err     error
		n       int64
	)

	switch o.State {
	case store.PRObservedOpen:
		head := o.HeadSHA

		changed, err = q.ObserveOpenPR(ctx, controlsdb.ObserveOpenPRParams{HeadSha: &head, ID: o.RemediationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
	case store.PRObservedMerged:
		n, err = q.ObserveMergedPR(ctx, controlsdb.ObserveMergedPRParams{ClosedAt: o.ClosedAt, ID: o.RemediationID})
		changed = n > 0
	case store.PRObservedClosed:
		n, err = q.ObserveClosedPR(ctx, controlsdb.ObserveClosedPRParams{ClosedAt: o.ClosedAt, ID: o.RemediationID})
		changed = n > 0
	default:
		return fmt.Errorf("unknown observation state %q", o.State)
	}

	if err != nil || !changed {
		return err
	}

	detail, err := json.Marshal(map[string]any{"pr_number": o.PRNumber, "state": o.State, "head_sha": o.HeadSHA})
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}

	return q.AppendRemediationEvent(ctx, controlsdb.AppendRemediationEventParams{
		RemediationID: o.RemediationID, Kind: remediationEventObserved, Detail: detail,
	})
}

// DueRemediationsFor is remediation_due for one repository, read by the
// evaluator after it records (IMPL-0030 2.5).
func (s *ControlsStore) DueRemediationsFor(ctx context.Context, repoID int64) ([]store.DueRemediation, error) {
	rows, err := s.q.DueRemediationsForRepository(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("due remediations for %d: %w", repoID, err)
	}

	out := make([]store.DueRemediation, 0, len(rows))
	for _, r := range rows {
		out = append(out, store.DueRemediation{RepositoryID: r.RepositoryID, ControlID: r.ControlID, Ordinal: int(r.Ordinal)})
	}

	return out, nil
}
