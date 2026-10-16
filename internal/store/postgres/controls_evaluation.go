package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

const (
	outcomePending   = "pending"
	reasonStaleEpoch = "stale_epoch"
)

// StageEvaluation writes the evaluation's payload to checks.pending_result
// under its check key. Only the selected controls are staged.
func (s *ControlsStore) StageEvaluation(ctx context.Context, st *store.EvaluationStage) error {
	payload, err := json.Marshal(st.Controls)
	if err != nil {
		return fmt.Errorf("stage %s: %w", st.CheckKey, err)
	}

	if err := s.q.StageCheck(ctx, controlsdb.StageCheckParams{
		RepositoryID: st.RepositoryID, CheckKey: st.CheckKey, Trigger: st.Trigger, PendingResult: payload,
		PolicyVersion: st.PolicyVersion, StartedAt: st.StartedAt,
	}); err != nil {
		return fmt.Errorf("stage %s: %w", st.CheckKey, err)
	}

	return nil
}

// RecordEvaluation commits a staged evaluation in one transaction. Each
// control is fenced on its assignment (DESIGN-0030 D14): unless the
// assignment is still active at the staged epoch, the result is
// discarded, logged and counted. A recorded check key returns
// AlreadyFinal and writes nothing.
func (s *ControlsStore) RecordEvaluation(ctx context.Context, checkKey string) (store.RecordOutcome, error) {
	var out store.RecordOutcome

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		out = store.RecordOutcome{}

		chk, err := q.LockCheck(ctx, checkKey)
		if err != nil {
			return fmt.Errorf("read check: %w", err)
		}

		if chk.Outcome != outcomePending {
			out.AlreadyFinal = true

			return nil
		}

		var staged []store.ControlEvaluation
		if err := json.Unmarshal(chk.PendingResult, &staged); err != nil {
			return fmt.Errorf("decode staged payload: %w", err)
		}

		for i := range staged {
			c := &staged[i]

			recorded, changed, err := s.recordControl(ctx, q, chk.RepositoryID, c)
			if err != nil {
				return fmt.Errorf("%s: %w", c.ControlID, err)
			}

			switch {
			case !recorded:
				out.Discarded = append(out.Discarded, c.ControlID)
			case changed:
				out.Recorded, out.Changed = append(out.Recorded, c.ControlID), append(out.Changed, c.ControlID)
			default:
				out.Recorded = append(out.Recorded, c.ControlID)
			}
		}

		if err := q.FinishCheck(ctx, controlsdb.FinishCheckParams{ID: chk.ID, Outcome: outcomeSuccess}); err != nil {
			return fmt.Errorf("finish check: %w", err)
		}

		// Counted once the transaction can no longer roll the discard
		// back: a retry of a failed record would count it again.
		for _, id := range out.Discarded {
			s.logger.InfoContext(ctx, "discarding a stale control result", "check_key", checkKey, "control", id, "reason", reasonStaleEpoch)
			metrics.ResultsDiscardedTotal.WithLabelValues(chk.Org, reasonStaleEpoch).Inc()
		}

		return nil
	})
	if err != nil {
		return store.RecordOutcome{}, fmt.Errorf("record evaluation %s: %w", checkKey, err)
	}

	return out, nil
}

// recordControl writes one control's result if its assignment is still
// active at the staged epoch. The generation starts at 1 on the first
// row of an epoch and is bumped on a fingerprint change, which also
// writes result_events; evaluated_sha, evaluated_at and evidence refresh
// on every record.
func (s *ControlsStore) recordControl(
	ctx context.Context, q *controlsdb.Queries, repoID int64, c *store.ControlEvaluation,
) (recorded, changed bool, err error) {
	fence, current, err := fenced(ctx, q, repoID, c)
	if err != nil || !current {
		return false, false, err
	}

	prev, err := q.LockResult(ctx, controlsdb.LockResultParams{RepositoryID: repoID, ControlID: c.ControlID})
	first := errors.Is(err, pgx.ErrNoRows) || (err == nil && prev.AssignmentEpoch != c.Epoch)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, false, fmt.Errorf("read result: %w", err)
	}

	generation, lastChanged := prev.EvalGeneration, prev.LastChangedAt

	switch {
	case first:
		generation, changed = 1, true
	case prev.Fingerprint != c.Fingerprint:
		generation, changed = prev.EvalGeneration+1, true
	}

	var priorStatus *string
	if !first {
		priorStatus = &prev.Status
	}

	if changed {
		lastChanged = time.Now()
	}

	if err := q.UpsertResult(ctx, controlsdb.UpsertResultParams{
		RepositoryID: repoID, ControlID: c.ControlID, ControlVersion: int32(c.ControlVersion), //nolint:gosec // a control version
		Revision: c.Revision, AssignmentEpoch: c.Epoch, Status: c.Status, Fingerprint: c.Fingerprint,
		IntentRevision: intentRevision(c, generation, fence.EffectiveMode), EvalGeneration: generation,
		EvaluatedSha: c.EvaluatedSHA, LastChangedAt: lastChanged,
	}); err != nil {
		return false, false, fmt.Errorf("upsert result: %w", err)
	}

	if err := s.recordRules(ctx, q, repoID, c, generation, changed, priorStatus); err != nil {
		return false, false, err
	}

	return true, changed, nil
}

// fenced reads and share-locks the control's assignment, reporting
// whether it is still active at the staged epoch.
func fenced(ctx context.Context, q *controlsdb.Queries, repoID int64, c *store.ControlEvaluation) (controlsdb.AssignmentFenceRow, bool, error) {
	fence, err := q.AssignmentFence(ctx, controlsdb.AssignmentFenceParams{RepositoryID: repoID, ControlID: c.ControlID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fence, false, nil
	}

	if err != nil {
		return fence, false, fmt.Errorf("fence: %w", err)
	}

	return fence, fence.State == "active" && fence.Epoch == c.Epoch, nil
}

// recordRules replaces the control's rule rows and, on a generation bump,
// writes one event for the control status and one per rule whose status
// moved.
func (*ControlsStore) recordRules(
	ctx context.Context, q *controlsdb.Queries, repoID int64, c *store.ControlEvaluation, generation int64, changed bool, priorStatus *string,
) error {
	prior := make(map[string]string)

	if changed && priorStatus != nil {
		rows, err := q.ListRuleStatuses(ctx, controlsdb.ListRuleStatusesParams{RepositoryID: repoID, ControlID: c.ControlID})
		if err != nil {
			return fmt.Errorf("read rules: %w", err)
		}

		for _, r := range rows {
			prior[r.RuleID] = r.Status
		}
	}

	ev := controlsdb.InsertResultEventParams{
		RepositoryID: repoID, ControlID: c.ControlID, ControlVersion: int32(c.ControlVersion), //nolint:gosec // a control version
		Revision: c.Revision, AssignmentEpoch: c.Epoch, EvalGeneration: generation,
	}

	if changed && textOf(priorStatus) != c.Status {
		e := ev
		e.FromStatus, e.ToStatus = priorStatus, &c.Status

		if err := q.InsertResultEvent(ctx, e); err != nil {
			return fmt.Errorf("control event: %w", err)
		}
	}

	keep := make([]string, 0, len(c.Rules))

	for i := range c.Rules {
		r := &c.Rules[i]
		keep = append(keep, r.RuleID)

		var event *controlsdb.InsertResultEventParams

		if was, ok := prior[r.RuleID]; changed && (!ok || was != r.Status) {
			e := ev
			e.RuleID, e.ToStatus = &r.RuleID, &r.Status

			if ok {
				e.FromStatus = &was
			}

			event = &e
		}

		if err := recordRule(ctx, q, repoID, c.ControlID, r, event); err != nil {
			return err
		}
	}

	if err := q.DeleteRuleResultsNotIn(ctx, controlsdb.DeleteRuleResultsNotInParams{
		RepositoryID: repoID, ControlID: c.ControlID, Keep: keep,
	}); err != nil {
		return fmt.Errorf("prune rules: %w", err)
	}

	return nil
}

// recordRule upserts one rule row and, when given, its transition.
func recordRule(
	ctx context.Context, q *controlsdb.Queries, repoID int64, controlID string, r *store.RuleEvaluation, event *controlsdb.InsertResultEventParams,
) error {
	if err := q.UpsertRuleResult(ctx, controlsdb.UpsertRuleResultParams{
		RepositoryID: repoID, ControlID: controlID, RuleID: r.RuleID, Status: r.Status, Remediate: r.Remediate,
		Evidence: r.Evidence, EvidenceKind: r.EvidenceKind,
		EvidenceVersion: int16(r.EvidenceVersion), //nolint:gosec // a small schema version
	}); err != nil {
		return fmt.Errorf("rule %s: %w", r.RuleID, err)
	}

	if event == nil {
		return nil
	}

	if err := q.InsertResultEvent(ctx, *event); err != nil {
		return fmt.Errorf("rule event %s: %w", r.RuleID, err)
	}

	return nil
}

// intentRevision is DESIGN-0032's intent digest: what remediation should
// now do, sha256(epoch, generation, apply, effective mode, remediable,
// revision).
func intentRevision(c *store.ControlEvaluation, generation int64, effectiveMode string) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%d\x00%d\x00%s\x00%s\x00%t\x00%s", c.Epoch, generation, c.Apply, effectiveMode, c.Remediable, c.Revision))

	return hex.EncodeToString(h[:])
}
