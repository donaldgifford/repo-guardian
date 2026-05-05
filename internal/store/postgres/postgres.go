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

	"github.com/donaldgifford/repo-guardian/internal/store"
)

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
	const q = `
		SELECT installation_id, owner, repo,
		       last_checked_at, last_check_status, last_error, policy_version
		FROM repo_state
		WHERE installation_id = $1 AND owner = $2 AND repo = $3
	`

	row := s.pool.QueryRow(ctx, q, installationID, owner, repo)

	var (
		rs  store.RepoState
		ts  *time.Time
		err error
	)

	err = row.Scan(&rs.InstallationID, &rs.Owner, &rs.Repo, &ts, &rs.LastCheckStatus, &rs.LastError, &rs.PolicyVersion)
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
	const q = `
		INSERT INTO repo_state (
			installation_id, owner, repo,
			last_checked_at, last_check_status, last_error, policy_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (installation_id, owner, repo) DO UPDATE SET
			last_checked_at   = EXCLUDED.last_checked_at,
			last_check_status = EXCLUDED.last_check_status,
			last_error        = EXCLUDED.last_error,
			policy_version    = EXCLUDED.policy_version
	`

	_, err := s.pool.Exec(ctx, q,
		rs.InstallationID, rs.Owner, rs.Repo,
		rs.LastCheckedAt, rs.LastCheckStatus, rs.LastError, rs.PolicyVersion,
	)
	if err != nil {
		return fmt.Errorf("postgres.UpdateRepoState: exec: %w", err)
	}

	return nil
}

// StaleRepos returns up to limit rows whose last_checked_at is older
// than freshness OR whose policy_version differs from
// currentPolicyVersion. NULL last_checked_at sorts first to ensure
// never-checked repos are reconciled before stale ones.
func (s *Store) StaleRepos(ctx context.Context, freshness time.Duration, currentPolicyVersion string, limit int) ([]store.RepoState, error) {
	const q = `
		SELECT installation_id, owner, repo,
		       last_checked_at, last_check_status, last_error, policy_version
		FROM repo_state
		WHERE last_checked_at IS NULL
		   OR last_checked_at < $1
		   OR policy_version <> $2
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
