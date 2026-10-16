package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// ControlsStore reads and writes the controls chain's tables: policy
// activation (IMPL-0028, as the owner) and the evaluator's store.Controls
// (IMPL-0029, as rg_evaluator).
type ControlsStore struct {
	pool   *pgxpool.Pool
	q      *controlsdb.Queries
	host   string
	logger *slog.Logger
}

// ControlsOption configures a ControlsStore.
type ControlsOption func(*ControlsStore)

// WithControlsHost sets the provider host repositories are keyed under;
// the default is github.com.
func WithControlsHost(host string) ControlsOption {
	return func(s *ControlsStore) {
		if host != "" {
			s.host = host
		}
	}
}

// WithControlsLogger sets the logger a discarded result is reported on.
func WithControlsLogger(l *slog.Logger) ControlsOption {
	return func(s *ControlsStore) { s.logger = l }
}

// NewControlsStore returns a ControlsStore over pool, which must point at
// a database migrated by the controls chain.
func NewControlsStore(pool *pgxpool.Pool, opts ...ControlsOption) *ControlsStore {
	s := &ControlsStore{pool: pool, q: controlsdb.New(pool), host: defaultHost, logger: slog.Default()}
	for _, o := range opts {
		o(s)
	}

	return s
}

// inTx runs fn in one transaction over the store's queries.
func (s *ControlsStore) inTx(ctx context.Context, fn func(q *controlsdb.Queries) error) error {
	if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return fn(s.q.WithTx(tx)) }); err != nil {
		return err //nolint:wrapcheck // callers wrap with their operation
	}

	return nil
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
