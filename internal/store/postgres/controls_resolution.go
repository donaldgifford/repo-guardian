package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

var _ store.Controls = (*ControlsStore)(nil)

// The assignment_changed event and its detail keys.
const (
	eventAssignmentChanged = "assignment_changed"
	detailControl          = "control"
	detailFrom             = "from"
	detailTo               = "to"
)

// UpsertInstallation refreshes one App installation's facts. As
// rg_evaluator only an 'eval' row is written; the policy fences the
// other App's rows.
func (s *ControlsStore) UpsertInstallation(ctx context.Context, in store.AppInstallation) error {
	err := s.q.UpsertAppInstallation(ctx, controlsdb.UpsertAppInstallationParams{
		App: in.App, InstallationID: in.InstallationID, AccountLogin: in.AccountLogin,
		SuspendedAt: in.SuspendedAt, RepositorySelection: in.RepositorySelection,
	})
	if err != nil {
		return fmt.Errorf("upsert %s installation %d: %w", in.App, in.InstallationID, err)
	}

	return nil
}

// UpsertRepositories writes each repository and its 'eval' access row,
// un-parks a parked one (discovery is the only un-parker), and resolves
// it, all in one transaction (DESIGN-0030 D19).
func (s *ControlsStore) UpsertRepositories(
	ctx context.Context, repos []store.DiscoveredRepository, r store.Resolver,
) ([]store.ResolvedRepository, error) {
	out := make([]store.ResolvedRepository, 0, len(repos))

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		for i := range repos {
			res, err := s.upsertOne(ctx, q, &repos[i], r)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", repos[i].Org, repos[i].Name, err)
			}

			out = append(out, res)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("upsert repositories: %w", err)
	}

	return out, nil
}

func (s *ControlsStore) upsertOne(
	ctx context.Context, q *controlsdb.Queries, d *store.DiscoveredRepository, r store.Resolver,
) (store.ResolvedRepository, error) {
	out := store.ResolvedRepository{Org: d.Org, Name: d.Name}
	providerID := &d.ProviderRepoID

	row, err := q.FindRepositoryByProviderID(ctx, controlsdb.FindRepositoryByProviderIDParams{Host: s.host, ProviderRepoID: providerID})
	if errors.Is(err, pgx.ErrNoRows) {
		var byName controlsdb.FindRepositoryByNameRow

		byName, err = q.FindRepositoryByName(ctx, controlsdb.FindRepositoryByNameParams{Host: s.host, Org: d.Org, Name: d.Name})
		row = controlsdb.FindRepositoryByProviderIDRow(byName)
	}

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.Created = true

		if out.ID, err = q.InsertRepository(ctx, controlsdb.InsertRepositoryParams{
			Host: s.host, Org: d.Org, Name: d.Name, ProviderRepoID: providerID,
		}); err != nil {
			return out, fmt.Errorf("insert: %w", err)
		}

		if err := event(ctx, q, out.ID, "discovered", nil); err != nil {
			return out, err
		}
	case err != nil:
		return out, fmt.Errorf("find: %w", err)
	default:
		out.ID = row.ID
		if err := q.UpdateRepositoryIdentity(ctx, controlsdb.UpdateRepositoryIdentityParams{
			ID: row.ID, Org: d.Org, Name: d.Name, ProviderRepoID: providerID,
		}); err != nil {
			return out, fmt.Errorf("update identity: %w", err)
		}

		if !row.Active {
			out.Unparked = true

			if err := q.UnparkRepository(ctx, row.ID); err != nil {
				return out, fmt.Errorf("unpark: %w", err)
			}

			if err := event(ctx, q, row.ID, "unparked", map[string]any{"park_reason": textOf(row.ParkReason)}); err != nil {
				return out, err
			}
		}
	}

	if err := q.UpsertAppRepositoryAccess(ctx, controlsdb.UpsertAppRepositoryAccessParams{
		RepositoryID: out.ID, App: store.AppEval, InstallationID: d.InstallationID,
	}); err != nil {
		return out, fmt.Errorf("access: %w", err)
	}

	repo := policy.Repository{Org: d.Org, Name: d.Name, Active: true, Archived: d.Archived, Fork: d.Fork}

	res, change, err := s.resolveWith(ctx, q, out.ID, repo, r)
	if err != nil {
		return out, err
	}

	out.State, out.Change = res.State, change

	return out, nil
}

// Resolve re-resolves one stored repository: a policy rollout, or a
// change to either App's access.
func (s *ControlsStore) Resolve(ctx context.Context, repoID int64, r store.Resolver) (store.ResolutionChange, error) {
	var change store.ResolutionChange

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		row, err := q.GetRepository(ctx, repoID)
		if err != nil {
			return fmt.Errorf("read repository: %w", err)
		}

		repo := policy.Repository{Org: row.Org, Name: row.Name, Active: row.Active, ParkReason: textOf(row.ParkReason)}
		_, change, err = s.resolveWith(ctx, q, repoID, repo, r)

		return err
	})
	if err != nil {
		return store.ResolutionChange{}, fmt.Errorf("resolve repository %d: %w", repoID, err)
	}

	return change, nil
}

// resolveWith reads both Apps' access, resolves and applies.
func (s *ControlsStore) resolveWith(
	ctx context.Context, q *controlsdb.Queries, repoID int64, repo policy.Repository, r store.Resolver,
) (policy.Resolution, store.ResolutionChange, error) {
	rows, err := q.RepositoryAccess(ctx, repoID)
	if err != nil {
		return policy.Resolution{}, store.ResolutionChange{}, fmt.Errorf("read access: %w", err)
	}

	res := r.Resolve(repo, accessOf(rows))

	change, err := s.apply(ctx, q, repoID, r.Version(), &res)

	return res, change, err
}

// accessOf derives DESIGN-0030 D15's per-repository booleans: evaluable
// when the Evaluation App reaches the repository unsuspended, remediable
// when the Remediation App does.
func accessOf(rows []controlsdb.RepositoryAccessRow) policy.AppAccess {
	var a policy.AppAccess

	for _, r := range rows {
		live := r.SuspendedAt == nil

		switch r.App {
		case store.AppEval:
			a.Evaluable = live
		case store.AppRemediate:
			a.Remediable = live
			a.RemediationSuspended = !live
		}
	}

	return a
}

// ApplyResolution persists res in its own transaction.
func (s *ControlsStore) ApplyResolution(ctx context.Context, repoID int64, version string, res policy.Resolution) (store.ResolutionChange, error) {
	var change store.ResolutionChange

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		var err error

		change, err = s.apply(ctx, q, repoID, version, &res)

		return err
	})
	if err != nil {
		return store.ResolutionChange{}, fmt.Errorf("apply resolution to repository %d: %w", repoID, err)
	}

	return change, nil
}

// apply is DESIGN-0030 step 7 and D6: the policy state is rewritten; an
// unchanged assignment keeps its epoch while a new or changed one takes
// the next from the sequence; a withdrawn or excluded assignment's
// result is deleted with a to_status = NULL event; every assignment
// change writes an assignment_changed event.
func (*ControlsStore) apply(
	ctx context.Context, q *controlsdb.Queries, repoID int64, version string, res *policy.Resolution,
) (store.ResolutionChange, error) {
	var change store.ResolutionChange

	if err := q.UpsertPolicyState(ctx, controlsdb.UpsertPolicyStateParams{
		RepositoryID: repoID, State: string(res.State), Reason: nullText(res.Reason),
		MaxOpenPrs:         int32(res.MaxOpenPRs), //nolint:gosec // a small policy setting
		ReopenAfterSeconds: res.ReopenAfter.Seconds(), PolicyVersion: version,
	}); err != nil {
		return change, fmt.Errorf("policy state: %w", err)
	}

	existing, err := q.ListAssignments(ctx, repoID)
	if err != nil {
		return change, fmt.Errorf("read assignments: %w", err)
	}

	prior := make(map[string]*controlsdb.ControlAssignment, len(existing))
	for i := range existing {
		prior[existing[i].ControlID] = &existing[i]
	}

	for i := range res.Assignments {
		a := &res.Assignments[i]
		old := prior[a.Control.Slug]
		delete(prior, a.Control.Slug)

		if err := applyAssignment(ctx, q, repoID, version, a, old, &change); err != nil {
			return change, err
		}
	}

	for _, id := range slices.Sorted(maps.Keys(prior)) {
		if err := removeAssignment(ctx, q, repoID, prior[id], &change); err != nil {
			return change, err
		}
	}

	return change, nil
}

// applyAssignment writes one resolved assignment, old being its stored
// row or nil.
func applyAssignment(ctx context.Context, q *controlsdb.Queries, repoID int64, version string, a *policy.Assignment,
	old *controlsdb.ControlAssignment, change *store.ResolutionChange,
) error {
	id := a.Control.Slug

	epoch, err := epochFor(ctx, q, repoID, a, old, change)
	if err != nil {
		return err
	}

	if err := q.UpsertAssignment(ctx, assignmentParams(repoID, version, epoch, a)); err != nil {
		return fmt.Errorf("assignment %s: %w", id, err)
	}

	if a.State != policy.AssignmentExcluded {
		return nil
	}

	return withdrawInto(ctx, q, repoID, id, change)
}

// epochFor keeps an unchanged assignment's epoch, and takes the next one
// from the sequence, with an assignment_changed event, for a new or
// changed assignment.
func epochFor(ctx context.Context, q *controlsdb.Queries, repoID int64, a *policy.Assignment,
	old *controlsdb.ControlAssignment, change *store.ResolutionChange,
) (int64, error) {
	if old != nil && sameAssignment(old, a) {
		return old.Epoch, nil
	}

	epoch, err := q.NextEpoch(ctx)
	if err != nil {
		return 0, fmt.Errorf("next epoch: %w", err)
	}

	var from string
	if old != nil {
		from = old.State
	}

	change.Changed = append(change.Changed, a.Control.Slug)

	return epoch, event(ctx, q, repoID, eventAssignmentChanged, map[string]any{
		detailControl: a.Control.String(), detailFrom: from, detailTo: string(a.State), "epoch": epoch,
	})
}

// removeAssignment deletes an assignment the resolution no longer holds
// and withdraws its result.
func removeAssignment(
	ctx context.Context, q *controlsdb.Queries, repoID int64, old *controlsdb.ControlAssignment, change *store.ResolutionChange,
) error {
	if err := q.DeleteAssignment(ctx, controlsdb.DeleteAssignmentParams{RepositoryID: repoID, ControlID: old.ControlID}); err != nil {
		return fmt.Errorf("delete assignment %s: %w", old.ControlID, err)
	}

	change.Changed = append(change.Changed, old.ControlID)

	if err := event(ctx, q, repoID, eventAssignmentChanged, map[string]any{
		detailControl: old.ControlID, detailFrom: old.State, detailTo: "withdrawn",
	}); err != nil {
		return err
	}

	return withdrawInto(ctx, q, repoID, old.ControlID, change)
}

// withdrawInto withdraws a result and notes it in change.
func withdrawInto(ctx context.Context, q *controlsdb.Queries, repoID int64, controlID string, change *store.ResolutionChange) error {
	gone, err := withdraw(ctx, q, repoID, controlID)
	if gone {
		change.Withdrawn = append(change.Withdrawn, controlID)
	}

	return err
}

// sameAssignment reports whether a stored assignment still matches its
// resolution on everything an epoch fences (D14): version, revision
// (which fixes the write set), both modes, source and state.
func sameAssignment(old *controlsdb.ControlAssignment, a *policy.Assignment) bool {
	return int(old.ControlVersion) == a.Control.Version &&
		old.Revision == a.Revision &&
		old.RequestedMode == string(a.RequestedMode) &&
		old.EffectiveMode == string(a.EffectiveMode) &&
		old.Source == a.Source &&
		old.State == string(a.State)
}

func assignmentParams(repoID int64, version string, epoch int64, a *policy.Assignment) controlsdb.UpsertAssignmentParams {
	return controlsdb.UpsertAssignmentParams{
		RepositoryID: repoID, ControlID: a.Control.Slug, ControlVersion: int32(a.Control.Version), //nolint:gosec // a control version
		Revision: a.Revision, State: string(a.State), Decision: string(a.Decision),
		BaselineControl: nullText(a.BaselineControl), Source: a.Source, SourceBlock: nullText(a.SourceBlock),
		Ordinal:  int32(a.Ordinal), //nolint:gosec // a position in policy
		Replaced: nullText(a.Replaced), ExcludedBy: nullText(a.ExcludedBy), Reason: nullText(a.Reason), Error: nullText(a.Error),
		RequestedMode: string(a.RequestedMode), EffectiveMode: string(a.EffectiveMode), ModeSource: a.ModeSource,
		ModeReason: nullText(a.ModeReason), Epoch: epoch, PolicyVersion: version,
	}
}

// withdraw deletes a control's result (cascading to rule_results) and
// records the withdrawal as a control-status event with a NULL
// to_status. It reports whether there was a result.
func withdraw(ctx context.Context, q *controlsdb.Queries, repoID int64, controlID string) (bool, error) {
	row, err := q.DeleteResult(ctx, controlsdb.DeleteResultParams{RepositoryID: repoID, ControlID: controlID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("withdraw result %s: %w", controlID, err)
	}

	if err := q.InsertResultEvent(ctx, controlsdb.InsertResultEventParams{
		RepositoryID: repoID, ControlID: controlID, ControlVersion: row.ControlVersion, Revision: row.Revision,
		AssignmentEpoch: row.AssignmentEpoch, FromStatus: &row.Status, EvalGeneration: row.EvalGeneration,
	}); err != nil {
		return false, fmt.Errorf("withdrawal event %s: %w", controlID, err)
	}

	return true, nil
}

func event(ctx context.Context, q *controlsdb.Queries, repoID int64, kind string, detail map[string]any) error {
	b := []byte(`{}`)

	if detail != nil {
		var err error
		if b, err = json.Marshal(detail); err != nil {
			return fmt.Errorf("%s event: %w", kind, err)
		}
	}

	if err := q.InsertRepositoryEvent(ctx, controlsdb.InsertRepositoryEventParams{RepositoryID: repoID, Kind: kind, Detail: b}); err != nil {
		return fmt.Errorf("%s event: %w", kind, err)
	}

	return nil
}

func nullText(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func textOf(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// parkClears is DESIGN-0030 D6's park rule: the reasons that are
// definitive non-applicability clear results; access_denied and unknown
// (the fail-safe) keep them. Evaluation App suspension is access state
// read from app_installations, never a park.
var parkClears = map[string]bool{
	"archived": true, "fork": true, "removed": true, "installation_removed": true,
	"access_denied": false, "unknown": false,
}

// Park parks a repository with reason. A reason in parkClears clears
// every result with a to_status = NULL event; the assignments stay,
// because they are desired state.
func (s *ControlsStore) Park(ctx context.Context, repoID int64, reason string) error {
	clears, ok := parkClears[reason]
	if !ok {
		return fmt.Errorf("park repository %d: unknown reason %q", repoID, reason)
	}

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		n, err := q.ParkRepository(ctx, controlsdb.ParkRepositoryParams{ID: repoID, ParkReason: &reason})
		if err != nil {
			return fmt.Errorf("park: %w", err)
		}

		if n > 0 {
			if err := event(ctx, q, repoID, "parked", map[string]any{"park_reason": reason}); err != nil {
				return err
			}
		}

		if !clears {
			return nil
		}

		ids, err := q.ListResultControls(ctx, repoID)
		if err != nil {
			return fmt.Errorf("read results: %w", err)
		}

		for _, id := range ids {
			if _, err := withdraw(ctx, q, repoID, id); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("park repository %d: %w", repoID, err)
	}

	return nil
}
