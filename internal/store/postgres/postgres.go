// Package postgres implements store.Store against PostgreSQL via
// pgx/v5 + pgxpool. Migrations are embedded under migrations/ and
// applied at binary startup by Migrate(); the binary fails fast if
// migrations don't apply.
//
// All queries are parameterized. The schema lives in
// migrations/0001_init.up.sql; see DESIGN-0012 §Data Model for the
// rationale behind the indexes.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// observeQuery records the duration of a single store query under
// (operation, outcome). outcome is "ok" if err is nil, "error"
// otherwise (including ErrNotFound, which we treat as a non-error
// signal but a non-OK observation for diagnostic purposes).
func observeQuery(operation string, start time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}

	metrics.StoreQuerySeconds.
		WithLabelValues(operation, outcome).
		Observe(time.Since(start).Seconds())
}

// Store implements store.Store against a *pgxpool.Pool. Construct via
// New; do not zero-init.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New connects a pool against dsn with maxConns as the connection
// cap. The caller is responsible for invoking Migrate(dsn) before
// New if the schema is not already at the current version (Migrate
// is exported separately so multi-replica deployments can run
// migrations as a one-shot Job and the runtime pods can skip the
// migration handshake).
func New(ctx context.Context, dsn string, maxConns int32, logger *slog.Logger) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres.New: parse DSN: %w", err)
	}

	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres.New: connect pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("postgres.New: ping: %w", err)
	}

	return &Store{pool: pool, logger: logger}, nil
}

// GetRepoState returns the persisted state for (installationID,
// owner, repo) or store.ErrNotFound if no row exists.
func (s *Store) GetRepoState(ctx context.Context, installationID int64, owner, repo string) (*store.RepoState, error) {
	start := time.Now()

	rs, err := s.getRepoStateInner(ctx, installationID, owner, repo)
	observeQuery("get_repo_state", start, err)

	return rs, err
}

func (s *Store) getRepoStateInner(ctx context.Context, installationID int64, owner, repo string) (*store.RepoState, error) {
	const q = `
		SELECT installation_id, owner, repo,
		       last_checked_at, last_check_status, last_error, policy_version,
		       catalog_parse_ok
		FROM repo_state
		WHERE installation_id = $1 AND owner = $2 AND repo = $3
	`

	row := s.pool.QueryRow(ctx, q, installationID, owner, repo)

	var (
		rs  store.RepoState
		ts  *time.Time
		err error
	)

	err = row.Scan(&rs.InstallationID, &rs.Owner, &rs.Repo, &ts,
		&rs.LastCheckStatus, &rs.LastError, &rs.PolicyVersion, &rs.CatalogParseOK)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("postgres.GetRepoState: scan: %w", err)
	}

	rs.LastCheckedAt = ts

	return &rs, nil
}

// UpdateRepoState upserts s. The (installation_id, owner, repo)
// primary key drives the conflict target; all other columns are
// overwritten on conflict.
func (s *Store) UpdateRepoState(ctx context.Context, rs *store.RepoState) error {
	start := time.Now()
	err := s.updateRepoStateInner(ctx, rs)
	observeQuery("update_repo_state", start, err)

	return err
}

func (s *Store) updateRepoStateInner(ctx context.Context, rs *store.RepoState) error {
	// COALESCE on catalog_parse_ok, not a plain EXCLUDED assignment:
	// a nil value means "this check learned nothing about the catalog"
	// (no catalog rule was evaluated), which must not erase a verdict a
	// previous check did establish. Every other column is unconditional
	// because every check re-establishes all of them.
	const q = `
		INSERT INTO repo_state (
			installation_id, owner, repo,
			last_checked_at, last_check_status, last_error, policy_version,
			catalog_parse_ok
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (installation_id, owner, repo) DO UPDATE SET
			last_checked_at   = EXCLUDED.last_checked_at,
			last_check_status = EXCLUDED.last_check_status,
			last_error        = EXCLUDED.last_error,
			policy_version    = EXCLUDED.policy_version,
			catalog_parse_ok  = COALESCE(EXCLUDED.catalog_parse_ok, repo_state.catalog_parse_ok)
	`

	_, err := s.pool.Exec(ctx, q,
		rs.InstallationID, rs.Owner, rs.Repo,
		rs.LastCheckedAt, rs.LastCheckStatus, rs.LastError, rs.PolicyVersion,
		rs.CatalogParseOK,
	)
	if err != nil {
		return fmt.Errorf("postgres.UpdateRepoState: exec: %w", err)
	}

	return nil
}

// UpsertRuleStates replaces the full set of rule verdicts for one
// repository in a single transaction: upsert every row in states, then
// delete any row for this repo whose rule name is not among them.
//
// The two halves must be atomic. A posture query that landed between
// them would see a repo with some rules deleted and others not yet
// written, and since the exporter aggregates across the whole fleet
// that shows up as a compliance percentage briefly dipping for no
// reason. The transaction is also why an empty states slice is a
// legitimate call rather than a no-op — it is how a repo that left
// policy scope stops counting.
func (s *Store) UpsertRuleStates(
	ctx context.Context,
	installationID int64,
	owner, repo string,
	states []store.RuleState,
) error {
	start := time.Now()
	err := s.upsertRuleStatesInner(ctx, installationID, owner, repo, states)
	observeQuery("upsert_rule_states", start, err)

	return err
}

func (s *Store) upsertRuleStatesInner(
	ctx context.Context,
	installationID int64,
	owner, repo string,
	states []store.RuleState,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres.UpsertRuleStates: begin: %w", err)
	}

	// Rollback after a successful Commit is a no-op that returns
	// ErrTxClosed, so this is safe unconditionally and guarantees the
	// transaction is released on every error path below.
	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // deferred best-effort cleanup; a no-op ErrTxClosed after Commit
	}()

	names := make([]string, 0, len(states))

	batch := &pgx.Batch{}

	for i := range states {
		st := &states[i]
		names = append(names, st.RuleName)
		batch.Queue(upsertRuleStateQuery,
			installationID, owner, repo,
			st.RuleName, st.RuleKind, st.Actionable, st.PolicyVersion,
		)
	}

	// Queued last so it runs after every upsert in the same round
	// trip. names is never NULL here — pgx encodes an empty slice as an
	// empty array, and `NOT (x = ANY('{}'))` is true for all x, which
	// is exactly the "clear every row" semantics an empty states slice
	// is supposed to have.
	batch.Queue(deleteRuleStatesNotInQuery, installationID, owner, repo, names)

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("postgres.UpsertRuleStates: batch: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres.UpsertRuleStates: commit: %w", err)
	}

	return nil
}

// upsertRuleStateQuery writes one rule verdict, managing the
// actionable_since transition entirely in SQL. Doing it here rather
// than in Go is what makes concurrent checks of the same repo safe:
// the CASE reads rule_state.actionable under the row lock ON CONFLICT
// already holds, so there is no read-then-write window in which two
// workers could both observe "was false" and both stamp a fresh
// timestamp.
//
// The three arms are the three edges that matter:
//   - false→true: the repo just started failing; stamp now().
//   - anything→false: the repo complies; clear it, so the next failure
//     starts a fresh clock instead of reporting a months-old date.
//   - true→true: still failing; preserve the original timestamp. This
//     is the whole point of the column — "missing since 2026-06-14"
//     must not reset to today on every sweep.
const upsertRuleStateQuery = `
	INSERT INTO rule_state (
		installation_id, owner, repo, rule_name, rule_kind,
		actionable, actionable_since, policy_version, updated_at
	) VALUES (
		$1, $2, $3, $4, $5,
		$6, CASE WHEN $6 THEN now() ELSE NULL END, $7, now()
	)
	ON CONFLICT (installation_id, owner, repo, rule_name) DO UPDATE SET
		actionable = EXCLUDED.actionable,
		actionable_since = CASE
			WHEN NOT rule_state.actionable AND EXCLUDED.actionable THEN now()
			WHEN NOT EXCLUDED.actionable                           THEN NULL
			ELSE rule_state.actionable_since
		END,
		rule_kind      = EXCLUDED.rule_kind,
		policy_version = EXCLUDED.policy_version,
		updated_at     = now()
`

// deleteRuleStatesNotInQuery reconciles away rows for rules that were
// not part of this check's evaluated set — a rule renamed, deleted from
// the policy, or newly scoped away from this repo (DESIGN-0022 OQ3 →
// a). Without it a removed rule would keep its last verdict forever and
// go on failing a compliance report nobody can fix.
const deleteRuleStatesNotInQuery = `
	DELETE FROM rule_state
	WHERE installation_id = $1 AND owner = $2 AND repo = $3
	  AND NOT (rule_name = ANY($4))
`

// UpsertIfMissing inserts rs as a discovery row (LastCheckedAt=nil,
// LastCheckStatus=StatusPending, LastError="") iff no row exists for
// (installation_id, owner, repo). It returns (true, nil) when a row
// was inserted and (false, nil) when an existing row was found.
//
// Implementation note: the `xmax` system column is 0 on a freshly
// inserted row and non-zero on a row matched by ON CONFLICT, so
// `(xmax = 0) AS created` gives a single-round-trip "did we insert?"
// answer. INSERT ... ON CONFLICT DO NOTHING returns no row on
// conflict by default, but we want a guaranteed RETURNING row so the
// query is wrapped in a WITH cte that does the insert and a SELECT
// against the existing row, UNIONed on the conflict path.
func (s *Store) UpsertIfMissing(ctx context.Context, rs *store.RepoState) (bool, error) {
	start := time.Now()

	created, err := s.upsertIfMissingInner(ctx, rs)
	observeQuery("upsert_if_missing", start, err)

	return created, err
}

func (s *Store) upsertIfMissingInner(ctx context.Context, rs *store.RepoState) (bool, error) {
	// The ON CONFLICT arm reactivates: discovery seeing a repository is
	// proof the App can reach it again, so a row parked by the
	// access-denied circuit breaker rejoins the sweep with no operator
	// action (INV-0015). The WHERE keeps this a no-op write for rows that
	// are already active, so a discovery pass over an untouched fleet
	// still costs nothing.
	//
	// It does NOT touch last_checked_at or policy_version: overwriting
	// those would reset the freshness gate and re-check the whole fleet
	// on every discovery pass.
	//
	// Single-statement upsert-or-discover. The trailing SELECT runs only
	// when the INSERT was suppressed by ON CONFLICT DO NOTHING (i.e. the
	// row already existed) — UNION ALL on the empty-ins case returns the
	// existing row's (xmax = 0) value, which is false because xmax is
	// the lock holder's XID on a previously inserted row.
	const q = `
		WITH ins AS (
			INSERT INTO repo_state (
				installation_id, owner, repo,
				last_checked_at, last_check_status, last_error, policy_version
			) VALUES ($1, $2, $3, NULL, $4, '', $5)
			ON CONFLICT (installation_id, owner, repo) DO UPDATE
				SET active = true
				WHERE NOT repo_state.active
			RETURNING (xmax = 0) AS created
		)
		SELECT created FROM ins
		UNION ALL
		SELECT false
		FROM repo_state
		WHERE installation_id = $1 AND owner = $2 AND repo = $3
		  AND NOT EXISTS (SELECT 1 FROM ins)
		LIMIT 1
	`

	status := rs.LastCheckStatus
	if status == "" {
		status = store.StatusPending
	}

	row := s.pool.QueryRow(ctx, q,
		rs.InstallationID, rs.Owner, rs.Repo,
		status, rs.PolicyVersion,
	)

	var created bool
	if err := row.Scan(&created); err != nil {
		return false, fmt.Errorf("postgres.UpsertIfMissing: scan: %w", err)
	}

	return created, nil
}

// Deactivate parks a repository so the sweep stops returning it.
// One-way by design: only discovery may reactivate a row, via
// UpsertIfMissing (INV-0015).
func (s *Store) Deactivate(ctx context.Context, installationID int64, owner, repo string) error {
	start := time.Now()

	const q = `UPDATE repo_state SET active = false
	           WHERE installation_id = $1 AND owner = $2 AND repo = $3`

	_, err := s.pool.Exec(ctx, q, installationID, owner, repo)
	observeQuery("deactivate", start, err)

	if err != nil {
		return fmt.Errorf("deactivating %s/%s: %w", owner, repo, err)
	}

	return nil
}

// StaleRepos returns up to limit ACTIVE rows whose last_checked_at is
// older than freshness OR whose policy_version differs from
// currentPolicyVersion. NULL last_checked_at sorts first to ensure
// never-checked repos are reconciled before stale ones. Inactive rows
// are never returned; see Deactivate.
func (s *Store) StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]store.RepoState, error) {
	start := time.Now()

	out, err := s.staleReposInner(ctx, freshness, currentPolicyVersion, limit)
	observeQuery("stale_repos", start, err)

	return out, err
}

func (s *Store) staleReposInner(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]store.RepoState, error) {
	const q = `
		SELECT installation_id, owner, repo,
		       last_checked_at, last_check_status, last_error, policy_version
		FROM repo_state
		WHERE active
		  AND (last_checked_at IS NULL
		       OR last_checked_at < $1
		       OR policy_version <> $2)
		ORDER BY last_checked_at NULLS FIRST
		LIMIT $3
	`

	cutoff := time.Now().Add(-freshness)

	rows, err := s.pool.Query(ctx, q, cutoff, currentPolicyVersion, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres.StaleRepos: query: %w", err)
	}
	defer rows.Close()

	out := make([]store.RepoState, 0, limit)

	for rows.Next() {
		var (
			rs store.RepoState
			ts *time.Time
		)

		if err := rows.Scan(&rs.InstallationID, &rs.Owner, &rs.Repo, &ts, &rs.LastCheckStatus, &rs.LastError, &rs.PolicyVersion); err != nil {
			return nil, fmt.Errorf("postgres.StaleRepos: scan: %w", err)
		}

		rs.LastCheckedAt = ts
		out = append(out, rs)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.StaleRepos: rows: %w", err)
	}

	return out, nil
}

// Close drains the connection pool. Idempotent.
func (s *Store) Close() error {
	if s.pool == nil {
		return nil
	}

	s.pool.Close()
	s.pool = nil

	if s.logger != nil {
		s.logger.Info("postgres store pool closed")
	}

	return nil
}
