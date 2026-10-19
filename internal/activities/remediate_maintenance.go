package activities

import (
	"context"
	"errors"
	"fmt"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/repo-guardian/internal/control"
	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// What one maintenance candidate came to.
const (
	maintSuperseded = "superseded"
	maintWithdrawn  = "withdrawn"
	maintClosed     = "closed"
	maintBlocked    = "blocked"
	maintSkipped    = "skipped"
)

// The sticky comment a withdrawn control's pull request closes with.
const withdrawnMessage = "This control no longer applies to this repository in remediate mode, so this pull request is no longer needed."

// Maintain is the sweep's lifecycle half (IMPL-0030 6.2): it frees the
// expired reservations, then works up to batch remediation_maintenance
// rows. A withdrawn control's PR closes as closed_withdrawn and its
// recommendation ends withdrawn; a moved recommendation is superseded; a
// compliant one closes only when a fresh read agrees. A refused write
// holds permission on the row (blocked cleanup). One candidate's failure
// is counted and the pass goes on; a throttle ends it.
func (s *RemediationServices) Maintain(ctx context.Context, batch int) (*workflows.MaintenanceResult, error) {
	orgs, err := s.cfg.Store.ExpireReservations(ctx, s.cfg.Remediator.now())
	if err != nil {
		return nil, fmt.Errorf("expire reservations: %w", err)
	}

	for _, org := range orgs {
		metrics.RemediationReservationsExpiredTotal.WithLabelValues(org).Inc()
	}

	res := &workflows.MaintenanceResult{Expired: len(orgs)}

	cands, err := s.cfg.Store.MaintenanceCandidates(ctx, batch)
	if err != nil {
		return nil, fmt.Errorf("maintenance candidates: %w", err)
	}

	for i := range cands {
		c := cands[i]

		activity.RecordHeartbeat(ctx, c.RemediationID)

		outcome, err := s.maintainOne(ctx, &c)
		if thr, throttled := ghclient.AsThrottled(err); throttled {
			s.cfg.Logger.Info("rate limit throttled; ending maintenance early", "remediation_id", c.RemediationID, "reset_at", thr.ResetAt)
			res.Throttled = true

			break
		}

		if err != nil {
			s.cfg.Logger.Warn("maintenance failed", "remediation_id", c.RemediationID, "action", c.Action, "error", err)
			res.Failed++

			continue
		}

		count(res, outcome)
	}

	return res, nil
}

func count(res *workflows.MaintenanceResult, outcome string) {
	switch outcome {
	case maintSuperseded:
		res.Superseded++
	case maintWithdrawn:
		res.Withdrawn++
	case maintClosed:
		res.Closed++
	case maintBlocked:
		res.Blocked++
	default:
		res.Skipped++
	}
}

// maintainOne dispatches one candidate by its action.
func (s *RemediationServices) maintainOne(ctx context.Context, c *store.MaintenanceCandidate) (string, error) {
	if c.Action == store.MaintenanceExpire {
		// ExpireReservations freed it, or its run opened the PR since.
		return maintSkipped, nil
	}

	m, err := s.maintenanceRun(ctx, c)
	if err != nil || m == nil {
		return maintSkipped, err
	}

	var outcome string

	switch c.Action {
	case store.MaintenanceSupersede:
		outcome, err = m.end(ctx, store.StateSuperseded, "superseded", maintSuperseded)
	case store.MaintenanceWithdraw:
		outcome, err = m.withdraw(ctx)
	case store.MaintenanceCloseCompliant:
		outcome, err = m.closeCompliant(ctx)
	default:
		return maintSkipped, nil
	}

	// A moved assignment or a run holding the operation: the next pass
	// decides again.
	if errors.Is(err, ErrStaleEpoch) || errors.Is(err, store.ErrOperationHeld) {
		return maintSkipped, nil
	}

	return outcome, err
}

// maintenanceRun reads one candidate's row and its control as the
// remediator sees it; nil when the row is gone.
func (s *RemediationServices) maintenanceRun(ctx context.Context, c *store.MaintenanceCandidate) (*maintenanceRun, error) {
	mt, err := s.cfg.Store.MaintenanceTarget(ctx, c.RemediationID)
	if errors.Is(err, store.ErrRemediationNotFound) {
		return nil, nil //nolint:nilnil // a reservation expired under the pass
	}

	if err != nil {
		return nil, fmt.Errorf("maintenance target %d: %w", c.RemediationID, err)
	}

	t, err := s.cfg.Store.RemediationTarget(ctx, mt.RepositoryID, mt.ControlID)
	if err != nil {
		return nil, fmt.Errorf("maintenance target %d: %w", c.RemediationID, err)
	}

	run := &remediationRun{
		in: &workflows.RemediateInput{
			RepositoryID: mt.RepositoryID, Control: mt.ControlID, InstallationID: mt.InstallationID, OperationKey: mt.OperationKey,
		},
		target: t, store: s.cfg.Store,
		log: s.cfg.Logger.With("remediation_id", mt.RemediationID, "control", mt.ControlID, "action", c.Action),
	}

	return &maintenanceRun{remediationRun: run, s: s, mt: mt}, nil
}

// maintenanceRun is one lifecycle candidate: its row, the control as
// first read (nil once withdrawn), and the fence its writes go through.
type maintenanceRun struct {
	*remediationRun

	s  *RemediationServices
	mt *store.MaintenanceTarget
}

// fence re-reads the control: a withdrawn control assigned again, or an
// assignment that moved since the candidate was read, stops the cleanup
// before its next write.
func (m *maintenanceRun) fence(ctx context.Context) error {
	now, err := m.store.RemediationTarget(ctx, m.in.RepositoryID, m.in.Control)
	if err != nil {
		return fmt.Errorf("fence: %w", err)
	}

	switch {
	case now == nil && m.target == nil:
		return nil
	case now == nil || m.target == nil || !fenceEqual(m.target, now):
		return ErrStaleEpoch
	}

	return nil
}

// withdraw ends a withdrawn control's artifact: a recommendation ends
// withdrawn, an open PR closes as closed_withdrawn. A reservation is
// left to its run or to expiry.
func (m *maintenanceRun) withdraw(ctx context.Context) (string, error) {
	switch {
	case m.mt.State == store.StateRecommended:
		return m.end(ctx, store.StateWithdrawn, "withdrawn", maintWithdrawn)
	case m.mt.State == store.StateOpen && m.mt.Kind == store.RemediationPullRequest && m.mt.PRNumber != 0:
		return m.closePR(ctx)
	default:
		return maintSkipped, nil
	}
}

// end moves the claimed row from its candidate state to state, with no
// external write.
func (m *maintenanceRun) end(ctx context.Context, state, reason, outcome string) (string, error) {
	ended := false

	err := m.claim(ctx, m.mt.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()
		if row.State != m.mt.State {
			return nil
		}

		if err := m.fence(ctx); err != nil {
			return err
		}

		now := m.s.cfg.Remediator.now()
		row.State, row.ClosedAt, row.CloseReason, row.Hold = state, &now, reason, ""

		if err := op.Update(ctx, &row); err != nil {
			return fmt.Errorf("mark %s: %w", state, err)
		}

		ended = true

		return nil
	})
	if err != nil || !ended {
		return maintSkipped, err
	}

	m.event(ctx, m.mt.RemediationID, state, "{}")

	if outcome != maintSuperseded {
		m.observe()
	}

	return outcome, nil
}

// closePR comments on and closes a withdrawn control's PR; the branch is
// never deleted. A refused write blocks the cleanup.
func (m *maintenanceRun) closePR(ctx context.Context) (string, error) {
	if !m.mt.Remediable || m.mt.InstallationID == 0 {
		return m.block(ctx)
	}

	repo, err := m.s.cfg.Remediator.clients.Repo(ctx, m.mt.InstallationID, m.mt.Org, m.mt.Name)
	if err != nil {
		return maintSkipped, err
	}

	closed := false

	err = m.claim(ctx, m.mt.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()
		if row.State != store.StateOpen {
			return nil
		}

		body := StickyMarker + "\n" + withdrawnMessage + "\n"

		if err := m.fence(ctx); err != nil {
			return err
		}

		if err := repo.Writer().UpsertPRComment(ctx, row.PRNumber, StickyMarker, body); err != nil {
			return fmt.Errorf("comment on #%d: %w", row.PRNumber, err)
		}

		if err := m.fence(ctx); err != nil {
			return err
		}

		if err := repo.Writer().ClosePullRequest(ctx, row.PRNumber); err != nil {
			return fmt.Errorf("close #%d: %w", row.PRNumber, err)
		}

		m.journal(ctx, row.ID, &store.RemediationStep{Kind: stepPRClosed, PRNumber: row.PRNumber})

		now := m.s.cfg.Remediator.now()
		row.State, row.ClosedAt, row.CloseReason, row.Hold = store.StateClosedWithdrawn, &now, "withdrawn", ""

		if err := op.Update(ctx, &row); err != nil {
			return fmt.Errorf("mark closed: %w", err)
		}

		closed = true

		return nil
	})

	switch {
	case accessDenied(err):
		return m.block(ctx)
	case err != nil || !closed:
		return maintSkipped, err
	}

	m.event(ctx, m.mt.RemediationID, store.StateClosedWithdrawn, "{}")
	m.observe()

	return maintClosed, nil
}

// closeCompliant closes an artifact main no longer needs, decided on a
// fresh read through the Remediation App (never the stored status): an
// open PR closes as closed_compliant, a recommendation ends resolved.
func (m *maintenanceRun) closeCompliant(ctx context.Context) (string, error) {
	t := m.target
	if t == nil {
		// Withdrawn since the candidate was read: the next pass withdraws.
		return maintSkipped, nil
	}

	if !m.mt.Remediable || m.mt.InstallationID == 0 {
		return m.block(ctx)
	}

	p, err := m.fresh(ctx)
	if err != nil || p == nil {
		return m.refused(ctx, err)
	}

	ev, _, err := p.evaluate(ctx, p.mainSHA, nil)
	if err != nil {
		return m.refused(ctx, err)
	}

	if control.StatusOf(ev) != control.StatusCompliant {
		// The stored result is behind main; the next evaluation says so.
		return maintSkipped, nil
	}

	if m.mt.State == store.StateRecommended {
		return m.end(ctx, store.StateResolved, closeCompliantReason, maintClosed)
	}

	if m.mt.State != store.StateOpen || m.mt.PRNumber == 0 {
		return maintSkipped, nil
	}

	closed := false

	err = m.claim(ctx, m.mt.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()
		if row.State != store.StateOpen {
			return nil
		}

		if err := p.closeCompliant(ctx, op, &row, row.PRNumber); err != nil {
			return err
		}

		closed = true

		return nil
	})
	if err != nil || !closed {
		return m.refused(ctx, err)
	}

	m.observe()

	return maintClosed, nil
}

// fresh opens the repository and pins main for a fresh read of the
// control at the assignment's revision; nil while the loaded policy
// carries another revision (the rollout re-resolves it).
func (m *maintenanceRun) fresh(ctx context.Context) (*prRun, error) {
	r := m.s.cfg.Remediator
	id := control.ID{Slug: m.mt.ControlID, Version: m.target.ControlVersion}

	if r.policy == nil || r.policy.Control(id) == nil || r.policy.Revision(id) != m.target.Revision {
		return nil, nil //nolint:nilnil // no control at this revision
	}

	m.control = r.policy.Control(id)
	m.def, _ = r.policy.Definition(id)

	repo, err := r.clients.Repo(ctx, m.mt.InstallationID, m.mt.Org, m.mt.Name)
	if err != nil {
		return nil, err
	}

	m.repo = repo

	main, sha, err := m.pinDefault(ctx)
	if err != nil {
		return nil, err
	}

	return &prRun{remediationRun: m.remediationRun, r: r, branch: branchName(m.mt.ControlID), main: main, mainSHA: sha}, nil
}

// refused blocks the cleanup on a refused read or write and passes any
// other error through.
func (m *maintenanceRun) refused(ctx context.Context, err error) (string, error) {
	if accessDenied(err) {
		return m.block(ctx)
	}

	return maintSkipped, err
}

// block holds permission on the row: blocked cleanup, shown as such
// until the Remediation App's access returns.
func (m *maintenanceRun) block(ctx context.Context) (string, error) {
	err := m.claim(ctx, m.mt.OperationKey, func(ctx context.Context, op store.ClaimedOperation) error {
		row := op.Remediation()
		if row.Hold == store.HoldPermission {
			return nil
		}

		row.Hold = store.HoldPermission

		return op.Update(ctx, &row)
	})
	if err != nil {
		return maintSkipped, err
	}

	m.log.Info("cleanup blocked: the remediation app cannot reach the repository")

	return maintBlocked, nil
}

// observe records remediation_cleanup_latency_seconds from the observed
// withdrawal or compliance to the closing write (IMPL-0030 6.3).
func (m *maintenanceRun) observe() {
	if m.mt.ObservedAt.IsZero() {
		return
	}

	metrics.RemediationCleanupLatencySeconds.WithLabelValues(m.mt.Org).Observe(m.s.cfg.Remediator.now().Sub(m.mt.ObservedAt).Seconds())
}
