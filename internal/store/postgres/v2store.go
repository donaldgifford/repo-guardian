package postgres

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// Check outcomes as stored in checks.outcome.
const (
	outcomeSuccess = "success"
	outcomeError   = "error"
)

// ErrNoStagedCheck is returned by RecordCheck when a CheckRecord carries
// no outcomes and no pending row was staged under its key.
var ErrNoStagedCheck = errors.New("no staged check for key")

// ErrConflict is returned when a concurrent transaction wrote the same
// check key or repository first. A retry sees the winner's row: for
// RecordCheck it returns the stored transitions.
var ErrConflict = errors.New("concurrent write won")

// V2Store implements store.Writer and store.Reader over the v2 schema
// (DESIGN-0025). The caller owns the pool. The hand-written store owns
// every transaction and wraps the sqlc-generated queries.
type V2Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewV2Store returns a V2Store over pool. The schema must already be
// migrated; see RequireSchema.
func NewV2Store(pool *pgxpool.Pool, logger *slog.Logger) *V2Store {
	return &V2Store{pool: pool, logger: logger}
}

// inTx runs fn in one transaction and commits when fn returns nil.
func (s *V2Store) inTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	// Rollback after Commit is a no-op; its error carries nothing new.
	defer tx.Rollback(ctx) //nolint:errcheck // see above

	if err := fn(sqlcdb.New(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// outcomeRow is an outcome in its stored shape: evidence already
// marshalled. It is also the staged payload element, so inline and
// staged outcomes follow one path.
type outcomeRow struct {
	RuleKind    string          `json:"rule_kind"`
	RuleName    string          `json:"rule_name"`
	Status      string          `json:"status"`
	Reason      *string         `json:"reason,omitempty"`
	Remediation string          `json:"remediation"`
	Evidence    json.RawMessage `json:"evidence"`
}

// findingKey identifies a finding within one repository.
type findingKey struct {
	kind, name string
}

// StageCheck implements store.Writer.
func (s *V2Store) StageCheck(ctx context.Context, c *store.CheckRecord) (err error) {
	defer func(start time.Time) { observeQuery("v2_stage_check", start, err) }(time.Now())

	rows, err := toOutcomeRows(c.Outcomes)
	if err != nil {
		return fmt.Errorf("postgres.StageCheck: %w", err)
	}

	payload, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("postgres.StageCheck: marshal payload: %w", err)
	}

	err = sqlcdb.New(s.pool).StageCheck(ctx, sqlcdb.StageCheckParams{
		RepositoryID:  c.RepositoryID,
		CheckKey:      c.Key,
		Trigger:       string(c.Trigger),
		PendingResult: payload,
		PolicyVersion: c.PolicyVersion,
		StartedAt:     c.StartedAt,
	})
	if err != nil {
		return fmt.Errorf("postgres.StageCheck: %w", err)
	}

	return nil
}

// RecordCheck implements store.Writer, following the DESIGN-0025
// "Recording a check" flowchart. The findings are locked and diffed in
// Go (IMPL-0025 OQ5); "change" means status, reason or remediation.
func (s *V2Store) RecordCheck(ctx context.Context, c *store.CheckRecord) (applied *store.CheckApplied, err error) {
	defer func(start time.Time) { observeQuery("v2_record_check", start, err) }(time.Now())

	now := c.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	err = s.inTx(ctx, func(q *sqlcdb.Queries) error {
		var txErr error

		applied, txErr = recordCheckTx(ctx, q, c, now)

		return txErr
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.RecordCheck %q: %w", c.Key, err)
	}

	return applied, nil
}

func recordCheckTx(ctx context.Context, q *sqlcdb.Queries, c *store.CheckRecord, now time.Time) (*store.CheckApplied, error) {
	rows, final, err := resolveOutcomes(ctx, q, c)
	if err != nil {
		return nil, err
	}

	if final != nil {
		return final, nil
	}

	current, err := q.LockFindings(ctx, c.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("lock findings: %w", err)
	}

	checkID, err := q.FinalizeCheck(ctx, sqlcdb.FinalizeCheckParams{
		RepositoryID:  c.RepositoryID,
		CheckKey:      c.Key,
		Trigger:       string(c.Trigger),
		Outcome:       outcomeSuccess,
		PolicyVersion: c.PolicyVersion,
		StartedAt:     c.StartedAt,
		FinishedAt:    &now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}

	if err != nil {
		return nil, fmt.Errorf("finalize check: %w", err)
	}

	transitions, err := applyFindings(ctx, q, c, checkID, current, rows, now)
	if err != nil {
		return nil, err
	}

	if err := updateRepositoryAfterCheck(ctx, q, c, now); err != nil {
		return nil, err
	}

	return &store.CheckApplied{CheckID: checkID, Transitions: transitions}, nil
}

// resolveOutcomes returns the outcome rows to apply, or a non-nil
// CheckApplied when the key is already final.
func resolveOutcomes(ctx context.Context, q *sqlcdb.Queries, c *store.CheckRecord) ([]outcomeRow, *store.CheckApplied, error) {
	existing, err := q.LockCheckByKey(ctx, c.Key)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if c.Outcomes == nil {
			return nil, nil, ErrNoStagedCheck
		}
	case err != nil:
		return nil, nil, fmt.Errorf("lock check: %w", err)
	case existing.Outcome != "pending":
		events, err := q.ListFindingEventsByCheck(ctx, &existing.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("read stored transitions: %w", err)
		}

		return nil, &store.CheckApplied{CheckID: existing.ID, Transitions: eventsToTransitions(events), AlreadyFinal: true}, nil
	case c.Outcomes == nil:
		var staged []outcomeRow
		if err := json.Unmarshal(existing.PendingResult, &staged); err != nil {
			return nil, nil, fmt.Errorf("decode staged outcomes: %w", err)
		}

		sortOutcomeRows(staged)

		return staged, nil, nil
	}

	rows, err := toOutcomeRows(c.Outcomes)
	if err != nil {
		return nil, nil, err
	}

	return rows, nil, nil
}

// applyFindings upserts every outcome, deletes findings no longer in
// the outcomes, and writes one event per created, changed or removed
// finding. It returns the transitions in event order.
func applyFindings(
	ctx context.Context,
	q *sqlcdb.Queries,
	c *store.CheckRecord,
	checkID int64,
	current []sqlcdb.Finding,
	rows []outcomeRow,
	now time.Time,
) ([]store.FindingTransition, error) {
	prior := make(map[findingKey]*sqlcdb.Finding, len(current))
	for i := range current {
		f := &current[i]
		prior[findingKey{f.RuleKind, f.RuleName}] = f
	}

	var events []sqlcdb.InsertFindingEventParams

	for i := range rows {
		r := &rows[i]
		key := findingKey{r.RuleKind, r.RuleName}
		old, existed := prior[key]
		delete(prior, key)

		since := now
		if existed && old.Status == r.Status {
			since = old.StatusSince
		}

		if err := q.UpsertFinding(ctx, sqlcdb.UpsertFindingParams{
			RepositoryID:    c.RepositoryID,
			RuleKind:        r.RuleKind,
			RuleName:        r.RuleName,
			Status:          r.Status,
			Reason:          r.Reason,
			Remediation:     r.Remediation,
			Evidence:        r.Evidence,
			EvidenceVersion: findings.EvidenceVersion,
			StatusSince:     since,
			LastEvaluatedAt: now,
			PolicyVersion:   c.PolicyVersion,
		}); err != nil {
			return nil, fmt.Errorf("upsert finding %s/%s: %w", r.RuleKind, r.RuleName, err)
		}

		switch {
		case !existed:
			events = append(events, createdEvent(r))
		case changed(old, r):
			events = append(events, changedEvent(old, r))
		}
	}

	removed := slices.SortedFunc(maps.Values(prior), compareFindings)
	for _, old := range removed {
		if err := q.DeleteFinding(ctx, sqlcdb.DeleteFindingParams{
			RepositoryID: c.RepositoryID, RuleKind: old.RuleKind, RuleName: old.RuleName,
		}); err != nil {
			return nil, fmt.Errorf("delete finding %s/%s: %w", old.RuleKind, old.RuleName, err)
		}

		events = append(events, removedEvent(old))
	}

	return writeEvents(ctx, q, c.RepositoryID, &checkID, c.PolicyVersion, now, events)
}

// updateRepositoryAfterCheck records the check on the repository row and
// refreshes its identity from the check's GetRepository. It never sets
// active: un-parking is discovery's alone.
func updateRepositoryAfterCheck(ctx context.Context, q *sqlcdb.Queries, c *store.CheckRecord, now time.Time) error {
	if c.Org != "" || c.Name != "" || c.ProviderRepoID != nil {
		row, err := q.LockRepository(ctx, c.RepositoryID)
		if err != nil {
			return fmt.Errorf("lock repository: %w", notFound(err))
		}

		if _, err := applyIdentity(ctx, q, &row, c.Org, c.Name, 0, c.ProviderRepoID); err != nil {
			return err
		}
	}

	if err := q.UpdateRepositoryAfterCheck(ctx, sqlcdb.UpdateRepositoryAfterCheckParams{
		ID:             c.RepositoryID,
		CheckedAt:      &now,
		PolicyVersion:  c.PolicyVersion,
		CatalogParseOk: c.CatalogParseOK,
	}); err != nil {
		return fmt.Errorf("update repository: %w", err)
	}

	if c.Rate == nil {
		return nil
	}

	if err := q.UpdateInstallationRate(ctx, sqlcdb.UpdateInstallationRateParams{
		InstallationID: c.InstallationID,
		RateLimit:      ptr(int32(c.Rate.Limit)),     //nolint:gosec // rate limits are small positive ints
		RateRemaining:  ptr(int32(c.Rate.Remaining)), //nolint:gosec // same
		RateResetAt:    &c.Rate.ResetAt,
		RateObservedAt: &c.Rate.ObservedAt,
	}); err != nil {
		return fmt.Errorf("update installation rate: %w", err)
	}

	return nil
}

// writeEvents inserts events in order and returns them as transitions.
func writeEvents(
	ctx context.Context,
	q *sqlcdb.Queries,
	repoID int64,
	checkID *int64,
	policyVersion string,
	now time.Time,
	events []sqlcdb.InsertFindingEventParams,
) ([]store.FindingTransition, error) {
	transitions := make([]store.FindingTransition, 0, len(events))

	for i := range events {
		e := &events[i]
		e.RepositoryID = repoID
		e.CheckID = checkID
		e.OccurredAt = now

		if e.PolicyVersion == "" {
			e.PolicyVersion = policyVersion
		}

		if err := q.InsertFindingEvent(ctx, *e); err != nil {
			return nil, fmt.Errorf("insert finding event %s/%s: %w", e.RuleKind, e.RuleName, err)
		}

		transitions = append(transitions, eventToTransition(e.RuleKind, e.RuleName,
			e.FromStatus, e.ToStatus, e.FromReason, e.ToReason, e.FromRemediation, e.ToRemediation))
	}

	return transitions, nil
}

func changed(old *sqlcdb.Finding, r *outcomeRow) bool {
	return old.Status != r.Status ||
		old.Remediation != r.Remediation ||
		derefOr(old.Reason) != derefOr(r.Reason)
}

func createdEvent(r *outcomeRow) sqlcdb.InsertFindingEventParams {
	return sqlcdb.InsertFindingEventParams{
		RuleKind:      r.RuleKind,
		RuleName:      r.RuleName,
		ToStatus:      &r.Status,
		ToReason:      r.Reason,
		ToRemediation: &r.Remediation,
		Evidence:      r.Evidence,
	}
}

func changedEvent(old *sqlcdb.Finding, r *outcomeRow) sqlcdb.InsertFindingEventParams {
	e := createdEvent(r)
	e.FromStatus = &old.Status
	e.FromReason = old.Reason
	e.FromRemediation = &old.Remediation

	return e
}

func removedEvent(old *sqlcdb.Finding) sqlcdb.InsertFindingEventParams {
	return sqlcdb.InsertFindingEventParams{
		RuleKind:        old.RuleKind,
		RuleName:        old.RuleName,
		FromStatus:      &old.Status,
		FromReason:      old.Reason,
		FromRemediation: &old.Remediation,
		Evidence:        json.RawMessage(`{}`),
	}
}

func eventsToTransitions(events []sqlcdb.FindingEvent) []store.FindingTransition {
	out := make([]store.FindingTransition, 0, len(events))
	for i := range events {
		e := &events[i]
		out = append(out, eventToTransition(e.RuleKind, e.RuleName,
			e.FromStatus, e.ToStatus, e.FromReason, e.ToReason, e.FromRemediation, e.ToRemediation))
	}

	return out
}

func eventToTransition(kind, name string, fromStatus, toStatus, fromReason, toReason, fromRem, toRem *string) store.FindingTransition {
	return store.FindingTransition{
		RuleKind:        findings.RuleKind(kind),
		RuleName:        name,
		FromStatus:      typedPtr[findings.Status](fromStatus),
		ToStatus:        typedPtr[findings.Status](toStatus),
		FromReason:      typedPtr[findings.Reason](fromReason),
		ToReason:        typedPtr[findings.Reason](toReason),
		FromRemediation: typedPtr[findings.Remediation](fromRem),
		ToRemediation:   typedPtr[findings.Remediation](toRem),
	}
}

// toOutcomeRows converts outcomes to their stored shape, sorted by
// (kind, name) so event order is deterministic.
func toOutcomeRows(outcomes []store.Outcome) ([]outcomeRow, error) {
	rows := make([]outcomeRow, 0, len(outcomes))

	for i := range outcomes {
		o := &outcomes[i]

		ev, err := marshalEvidence(o)
		if err != nil {
			return nil, fmt.Errorf("marshal evidence %s/%s: %w", o.RuleKind, o.RuleName, err)
		}

		var reason *string
		if o.Reason != findings.ReasonNone {
			reason = ptr(string(o.Reason))
		}

		remediation := o.Remediation
		if remediation == "" {
			remediation = findings.RemediationNone
		}

		rows = append(rows, outcomeRow{
			RuleKind:    string(o.RuleKind),
			RuleName:    o.RuleName,
			Status:      string(o.Status),
			Reason:      reason,
			Remediation: string(remediation),
			Evidence:    ev,
		})
	}

	sortOutcomeRows(rows)

	return rows, nil
}

func sortOutcomeRows(rows []outcomeRow) {
	slices.SortFunc(rows, func(a, b outcomeRow) int {
		return cmp.Or(cmp.Compare(a.RuleKind, b.RuleKind), cmp.Compare(a.RuleName, b.RuleName))
	})
}

func compareFindings(a, b *sqlcdb.Finding) int {
	return cmp.Or(cmp.Compare(a.RuleKind, b.RuleKind), cmp.Compare(a.RuleName, b.RuleName))
}

// marshalEvidence renders an outcome's evidence object, with any PR
// merged under the "pr" key (DESIGN-0025 § Reason codes and evidence).
func marshalEvidence(o *store.Outcome) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}

	if o.Evidence != nil {
		b, err := json.Marshal(o.Evidence)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(b, &obj); err != nil {
			return nil, fmt.Errorf("evidence must be a JSON object: %w", err)
		}
	}

	var pr any

	switch {
	case o.PR != nil:
		pr = o.PR
	case o.ForeignPR != nil:
		pr = o.ForeignPR
	}

	if pr != nil {
		b, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}

		obj["pr"] = b
	}

	return json.Marshal(obj)
}

func ptr[T any](v T) *T { return &v }

func derefOr(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func typedPtr[T ~string](s *string) *T {
	if s == nil {
		return nil
	}

	v := T(*s)

	return &v
}
