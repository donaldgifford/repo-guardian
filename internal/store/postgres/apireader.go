package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// APIReader is the api role's read side (DESIGN-0027) over the controls
// schema (IMPL-0029 7.4). Every method runs
// in its own read-only transaction and is scoped in SQL by a
// store.APIScope; nothing is filtered in Go after a query.
type APIReader struct {
	pool *pgxpool.Pool
}

// NewAPIReader returns an APIReader on pool, normally a NewReadOnlyPool.
func NewAPIReader(pool *pgxpool.Pool) *APIReader {
	return &APIReader{pool: pool}
}

// Ping checks the pool reaches the database.
func (r *APIReader) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

// readOnly runs fn in a read-only, repeatable-read transaction, so a
// view built from several queries reads one snapshot.
func (r *APIReader) readOnly(ctx context.Context, fn func(q *controlsdb.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return fmt.Errorf("begin read-only transaction: %w", err)
	}

	// Rollback after Commit is a no-op; its error carries nothing new.
	defer tx.Rollback(ctx) //nolint:errcheck // see above

	if err := fn(controlsdb.New(tx)); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
