package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// ControlsStore reads and writes the controls chain's tables. IMPL-0028
// gives it only policy activation; IMPL-0029 and IMPL-0030 grow it.
type ControlsStore struct {
	pool *pgxpool.Pool
	q    *controlsdb.Queries
}

// NewControlsStore returns a ControlsStore over pool, which must point at
// a database migrated by the controls chain.
func NewControlsStore(pool *pgxpool.Pool) *ControlsStore {
	return &ControlsStore{pool: pool, q: controlsdb.New(pool)}
}

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

	firstSeen, err = s.q.ActivatePolicyVersion(ctx, controlsdb.ActivatePolicyVersionParams{Version: version, Summary: summary})
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
	row, err := s.q.CurrentActivation(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, nil
	}

	if err != nil {
		return "", time.Time{}, fmt.Errorf("current policy activation: %w", err)
	}

	return row.Version, row.ActivatedAt, nil
}
