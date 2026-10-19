package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	openapitypes "github.com/oapi-codegen/runtime/types"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Pull request throughput window, in days (DESIGN-0037 § API).
const (
	defaultActivityDays = 14
	maxActivityDays     = 90
)

// remediationFilters is what a remediations cursor is bound to.
type remediationFilters struct {
	Kind      string   `json:"kind,omitempty"`
	State     string   `json:"state,omitempty"`
	Org       string   `json:"org,omitempty"`
	Control   string   `json:"control,omitempty"`
	Holds     []string `json:"hold,omitempty"`
	OlderThan int      `json:"older_than_days,omitempty"`
}

type remediationKey struct {
	At           time.Time `json:"at"`
	RepositoryID int64     `json:"r"`
	ControlID    string    `json:"c"`
	ID           int64     `json:"id"`
}

// ListRemediations returns one page of remediations over the visible
// orgs (IMPL-0030 7.4), with the held results when a branch hold is
// asked for (7.16).
func (s *server) ListRemediations(ctx context.Context, req gen.ListRemediationsRequestObject) (gen.ListRemediationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	limit, err := pageSize(q.Limit)
	if err != nil {
		return nil, err
	}

	filters := remediationFilters{Kind: deref(q.Kind), State: deref(q.State), Org: deref(q.Org), Control: deref(q.Control)}
	if q.Hold != nil {
		filters.Holds = *q.Hold
	}

	if q.OlderThanDays != nil {
		filters.OlderThan = *q.OlderThanDays
	}

	f := store.RemediationFilter{
		Kind: filters.Kind, State: filters.State, Org: filters.Org, ControlID: filters.Control, Holds: filters.Holds, Limit: limit,
	}

	if filters.OlderThan > 0 {
		before := s.opts.Now().Add(-time.Duration(filters.OlderThan) * 24 * time.Hour)
		f.Before = &before
	}

	var key remediationKey
	if ok, err := cursorIn(q.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		f.After = &store.RemediationKey{At: key.At, RepositoryID: key.RepositoryID, ControlID: key.ControlID, ID: key.ID}
	}

	page, err := s.opts.Reader.Remediations(ctx, p.Visible, &f)
	if err != nil {
		return nil, err
	}

	out := gen.ListRemediations200JSONResponse{Items: make([]gen.Remediation, 0, len(page.Items))}

	for i := range page.Items {
		r, err := remediationOut(&page.Items[i])
		if err != nil {
			return nil, err
		}

		out.Items = append(out.Items, r)
	}

	out.NextCursor, err = nextCursor(page, func(it *store.RemediationView) any {
		return remediationKey{At: it.CreatedAt, RepositoryID: it.RepositoryID, ControlID: it.ControlID, ID: it.ID}
	}, filters)

	return out, err
}

// GetRemediationActivity returns pull requests opened and closed per UTC
// day over the visible orgs (IMPL-0030 7.12).
func (s *server) GetRemediationActivity(
	ctx context.Context, req gen.GetRemediationActivityRequestObject,
) (gen.GetRemediationActivityResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	days := defaultActivityDays
	if req.Params.Days != nil {
		days = *req.Params.Days
	}

	if days < 1 || days > maxActivityDays {
		return nil, statusError(http.StatusBadRequest, "days must be from 1 to 90")
	}

	rows, err := s.opts.Reader.RemediationActivity(ctx, p.Visible, days)
	if err != nil {
		return nil, err
	}

	out := gen.GetRemediationActivity200JSONResponse{Items: make([]gen.ActivityDay, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, gen.ActivityDay{Day: openapitypes.Date{Time: r.Day}, Opened: r.Opened, Closed: r.Closed})
	}

	return out, nil
}

func remediationOut(r *store.RemediationView) (gen.Remediation, error) {
	out := gen.Remediation{
		RepositoryId: r.RepositoryID, Org: r.Org, Repository: r.Repository, ControlId: r.ControlID, Kind: r.Kind,
		State: optStr(r.State), IntentRevision: r.IntentRevision, Branch: optStr(r.Branch), PrUrl: optStr(r.PRURL),
		ObservedHeadSha: optStr(r.ObservedHeadSHA), AcknowledgedHeadSha: optStr(r.AcknowledgedHeadSHA),
		Hold: optStr(r.Hold), HoldSince: r.HoldSince, CreatedAt: r.CreatedAt, LastRunAt: r.LastRunAt,
		ClosedAt: r.ClosedAt, CloseReason: optStr(r.CloseReason), Steps: make([]gen.RemediationStep, 0, len(r.Steps)),
	}

	if r.ID != 0 {
		out.Id = &r.ID
	}

	if r.PRNumber != 0 {
		out.PrNumber = &r.PRNumber
	}

	if len(r.Proposal) > 0 && string(r.Proposal) != "null" {
		var proposal []gen.ProposedChange
		if err := json.Unmarshal(r.Proposal, &proposal); err != nil {
			return out, fmt.Errorf("remediation %d proposal: %w", r.ID, err)
		}

		out.Proposal = &proposal
	}

	for i := range r.Steps {
		st, err := remediationStepOut(&r.Steps[i])
		if err != nil {
			return out, err
		}

		out.Steps = append(out.Steps, st)
	}

	return out, nil
}

func remediationStepOut(s *store.RemediationStep) (gen.RemediationStep, error) {
	out := gen.RemediationStep{
		Seq: s.Seq, Kind: s.Kind, Status: s.Status, ResourceKey: optStr(s.ResourceKey), CommittedSha: optStr(s.CommittedSHA),
		Error: optStr(s.Error), OccurredAt: s.OccurredAt,
	}

	if s.PRNumber != 0 {
		out.PrNumber = &s.PRNumber
	}

	var err error

	if out.Before, err = anyJSON(s.Before); err != nil {
		return out, fmt.Errorf("step %d before: %w", s.Seq, err)
	}

	if out.After, err = anyJSON(s.After); err != nil {
		return out, fmt.Errorf("step %d after: %w", s.Seq, err)
	}

	return out, nil
}

// anyJSON decodes a stored JSON value; nil for none.
func anyJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // no value is not an error
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	return v, nil
}
