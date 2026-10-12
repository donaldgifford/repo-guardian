package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ControlsStore reads and writes the controls chain's tables. IMPL-0028
// gives it only policy activation; IMPL-0029 and IMPL-0030 grow it.
type ControlsStore struct {
	pool *pgxpool.Pool
}

// NewControlsStore returns a ControlsStore over pool, which must point at
// a database migrated by the controls chain.
func NewControlsStore(pool *pgxpool.Pool) *ControlsStore {
	return &ControlsStore{pool: pool}
}

// activatePolicyVersion is DESIGN-0030 D14's upsert (INV-0022 spike 8):
// a version seen before keeps its first_seen_at and summary, and is
// re-activated, so a revert to it is honoured.
//
// Re-activating the version that is already current changes nothing
// (IMPL-0028 8.4): Argo CD re-runs the migrate Job on every sync, and a
// fresh activated_at would start a rollout each time. The update then
// matches no row and RETURNING is empty.
const activatePolicyVersion = `INSERT INTO policy_versions (version, summary) VALUES ($1, $2)
ON CONFLICT (version) DO UPDATE SET activated_at = now()
  WHERE policy_versions.version IS DISTINCT FROM
    (SELECT version FROM policy_versions ORDER BY activated_at DESC, version DESC LIMIT 1)
RETURNING (xmax = 0) AS first_seen`

// ActivatePolicyVersion records version as the policy activated now and
// reports whether it had never been seen. It runs as the owner, from the
// migrate Job (DESIGN-0030 D14): the application roles cannot activate.
// summary is the policy's JSON summary, stored only on first sight; nil
// stores an empty object. Re-activating the current version is a no-op
// that reports false.
func (s *ControlsStore) ActivatePolicyVersion(ctx context.Context, version string, summary json.RawMessage) (firstSeen bool, err error) {
	if summary == nil {
		summary = json.RawMessage(`{}`)
	}

	err = s.pool.QueryRow(ctx, activatePolicyVersion, version, summary).Scan(&firstSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		// Already the current activation.
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("activate policy version %s: %w", version, err)
	}

	return firstSeen, nil
}

// CurrentActivation is the most recently activated policy version, which
// rollout follows. It returns an empty version when none was activated.
func (s *ControlsStore) CurrentActivation(ctx context.Context) (version string, activatedAt time.Time, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT version, activated_at FROM policy_versions ORDER BY activated_at DESC, version DESC LIMIT 1`).
		Scan(&version, &activatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, nil
	}

	if err != nil {
		return "", time.Time{}, fmt.Errorf("current policy activation: %w", err)
	}

	return version, activatedAt, nil
}
