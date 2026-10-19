package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// RemediationStore is the remediator's store.Remediations over the
// controls chain (IMPL-0030 Phase 1). Its pool connects as
// rg_remediator, whose grants are the write boundary.
type RemediationStore struct {
	pool *pgxpool.Pool
	q    *controlsdb.Queries
}

var _ store.Remediations = (*RemediationStore)(nil)

// NewRemediationStore returns a RemediationStore over pool, which must
// point at a database migrated by the controls chain.
func NewRemediationStore(pool *pgxpool.Pool) *RemediationStore {
	return &RemediationStore{pool: pool, q: controlsdb.New(pool)}
}

// ReserveSlot takes a pull-request slot in one transaction: lock the
// repository's policy-state row, count the open and reserved rows the
// cap covers, and insert a reservation or return store.ErrPRCap.
func (s *RemediationStore) ReserveSlot(ctx context.Context, in *store.SlotRequest) (*store.Remediation, error) {
	var out *store.Remediation

	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)

		// A retried activity finds its own reservation, cap or not.
		if row, err := q.RemediationByOperationKey(ctx, in.OperationKey); err == nil {
			out = remediationOut(&row)
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read operation: %w", err)
		}

		limit, err := q.LockPolicyStateForSlot(ctx, in.RepositoryID)
		if err != nil {
			return fmt.Errorf("lock policy state: %w", err)
		}

		used, err := q.CountSlots(ctx, in.RepositoryID)
		if err != nil {
			return fmt.Errorf("count slots: %w", err)
		}

		if used >= limit {
			return store.ErrPRCap
		}

		if _, err := q.InsertReservation(ctx, controlsdb.InsertReservationParams{
			RepositoryID:         in.RepositoryID,
			ControlID:            in.ControlID,
			AssignmentEpoch:      in.Epoch,
			OperationKey:         in.OperationKey,
			IntentRevision:       in.Intent,
			TtlSeconds:           in.TTL.Seconds(),
			RemediatedGeneration: in.Generation,
		}); err != nil {
			return fmt.Errorf("insert reservation: %w", err)
		}

		row, err := q.RemediationByOperationKey(ctx, in.OperationKey)
		if err != nil {
			return fmt.Errorf("read reservation: %w", err)
		}

		out = remediationOut(&row)

		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrPRCap) {
			return nil, store.ErrPRCap
		}

		return nil, fmt.Errorf("reserve slot: %w", err)
	}

	return out, nil
}

// ClaimOperation holds the operation's row with FOR NO KEY UPDATE SKIP
// LOCKED for the life of fn (DESIGN-0032 § Remediation: overlapping
// attempts). The transaction holds one pool connection, which is why the
// remediator's pool must be at least its concurrency (task 1.9).
func (s *RemediationStore) ClaimOperation(ctx context.Context, operationKey string, fn func(context.Context, store.ClaimedOperation) error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)

		row, err := q.ClaimRemediation(ctx, operationKey)
		if errors.Is(err, pgx.ErrNoRows) {
			// Locked by another attempt, or absent: SKIP LOCKED cannot
			// tell, so read without the lock.
			if _, err := s.q.RemediationByOperationKey(ctx, operationKey); errors.Is(err, pgx.ErrNoRows) {
				return store.ErrRemediationNotFound
			} else if err != nil {
				return fmt.Errorf("read operation: %w", err)
			}

			return store.ErrOperationHeld
		}

		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}

		return fn(ctx, &claimedOperation{q: q, row: *remediationOut(&row)})
	})

	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrOperationHeld), errors.Is(err, store.ErrRemediationNotFound):
		return err
	default:
		return fmt.Errorf("claim operation %s: %w", operationKey, err)
	}
}

// LatestRemediation is the newest row for one control, or nil.
func (s *RemediationStore) LatestRemediation(ctx context.Context, repositoryID int64, controlID string) (*store.Remediation, error) {
	row, err := s.q.LatestRemediation(ctx, controlsdb.LatestRemediationParams{RepositoryID: repositoryID, ControlID: controlID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // no remediation yet is not an error
	}

	if err != nil {
		return nil, fmt.Errorf("latest remediation: %w", err)
	}

	return remediationOut(&row), nil
}

// AppendStep journals one step on its own connection, so it commits even
// when the claimed operation later fails.
func (s *RemediationStore) AppendStep(ctx context.Context, step *store.RemediationStep) (int, error) {
	seq, err := s.q.AppendRemediationStep(ctx, controlsdb.AppendRemediationStepParams{
		RemediationID: step.RemediationID,
		OperationKey:  step.OperationKey,
		Kind:          step.Kind,
		ResourceKey:   nullText(step.ResourceKey),
		BlobSha:       nullText(step.BlobSHA),
		CommittedSha:  nullText(step.CommittedSHA),
		PrNumber:      optInt32(step.PRNumber),
		Status:        step.Status,
		Before:        step.Before,
		After:         step.After,
		Error:         nullText(step.Error),
	})
	if err != nil {
		return 0, fmt.Errorf("append step: %w", err)
	}

	return int(seq), nil
}

// Steps is a remediation's journal in sequence order.
func (s *RemediationStore) Steps(ctx context.Context, remediationID int64) ([]store.RemediationStep, error) {
	rows, err := s.q.ListRemediationSteps(ctx, remediationID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}

	out := make([]store.RemediationStep, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, store.RemediationStep{
			RemediationID: r.RemediationID,
			OperationKey:  r.OperationKey,
			Seq:           int(r.Seq),
			Kind:          r.Kind,
			ResourceKey:   textOf(r.ResourceKey),
			BlobSHA:       textOf(r.BlobSha),
			CommittedSHA:  textOf(r.CommittedSha),
			PRNumber:      intOf(r.PrNumber),
			Status:        r.Status,
			Before:        r.Before,
			After:         r.After,
			Error:         textOf(r.Error),
			OccurredAt:    r.OccurredAt,
		})
	}

	return out, nil
}

// AppendEvent records one remediation event; a nil detail stores {}.
func (s *RemediationStore) AppendEvent(ctx context.Context, remediationID int64, kind string, detail json.RawMessage) error {
	if detail == nil {
		detail = json.RawMessage(`{}`)
	}

	if err := s.q.AppendRemediationEvent(ctx, controlsdb.AppendRemediationEventParams{
		RemediationID: remediationID,
		Kind:          kind,
		Detail:        detail,
	}); err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	return nil
}

// claimedOperation writes through the claim's transaction, the only
// connection that can touch the locked row without blocking on itself.
type claimedOperation struct {
	q   *controlsdb.Queries
	row store.Remediation
}

func (c *claimedOperation) Remediation() store.Remediation { return c.row }

func (c *claimedOperation) Update(ctx context.Context, r *store.Remediation) error {
	if err := c.q.UpdateRemediation(ctx, controlsdb.UpdateRemediationParams{
		ID:             c.row.ID,
		State:          r.State,
		IntentRevision: r.IntentRevision,
		BaseSha:        nullText(r.BaseSHA),
		Branch:         nullText(r.Branch),
		ExpectedHead:   nullText(r.ExpectedHead),
		ReservedUntil:  r.ReservedUntil,
		PrNumber:       optInt32(r.PRNumber),
		PrUrl:          nullText(r.PRURL),
		Proposal:       r.Proposal,
		Hold:           nullText(r.Hold),
		ClosedAt:       r.ClosedAt,
		CloseReason:    nullText(r.CloseReason),
	}); err != nil {
		return fmt.Errorf("update remediation: %w", err)
	}

	next := *r
	next.ID, next.OperationKey = c.row.ID, c.row.OperationKey
	c.row = next

	return nil
}

func (c *claimedOperation) SetIntent(ctx context.Context, intentRevision string) error {
	if err := c.q.SetRemediationIntent(ctx, controlsdb.SetRemediationIntentParams{ID: c.row.ID, IntentRevision: intentRevision}); err != nil {
		return fmt.Errorf("set intent: %w", err)
	}

	c.row.IntentRevision = intentRevision

	return nil
}

func remediationOut(r *controlsdb.Remediation) *store.Remediation {
	return &store.Remediation{
		ID:                   r.ID,
		RepositoryID:         r.RepositoryID,
		ControlID:            r.ControlID,
		AssignmentEpoch:      r.AssignmentEpoch,
		OperationKey:         r.OperationKey,
		Kind:                 r.Kind,
		State:                r.State,
		IntentRevision:       r.IntentRevision,
		BaseSHA:              textOf(r.BaseSha),
		Branch:               textOf(r.Branch),
		ExpectedHead:         textOf(r.ExpectedHead),
		ReservedUntil:        r.ReservedUntil,
		PRNumber:             intOf(r.PrNumber),
		PRURL:                textOf(r.PrUrl),
		ObservedHeadSHA:      textOf(r.ObservedHeadSha),
		ObservedAt:           r.ObservedAt,
		AcknowledgedHeadSHA:  textOf(r.AcknowledgedHeadSha),
		Proposal:             r.Proposal,
		RemediatedGeneration: r.RemediatedGeneration,
		ClosedGeneration:     r.ClosedGeneration,
		NextEligibleAt:       r.NextEligibleAt,
		Hold:                 textOf(r.Hold),
		CreatedAt:            r.CreatedAt,
		LastRunAt:            r.LastRunAt,
		ClosedAt:             r.ClosedAt,
		CloseReason:          textOf(r.CloseReason),
	}
}

func optInt32(v int) *int32 {
	if v == 0 {
		return nil
	}

	n := int32(v) //nolint:gosec // PR numbers and sequence numbers fit int32 (the column type)

	return &n
}

func intOf(v *int32) int {
	if v == nil {
		return 0
	}

	return int(*v)
}
