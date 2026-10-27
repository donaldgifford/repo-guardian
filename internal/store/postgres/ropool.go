package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The api role's pool limits (DESIGN-0027 § The api role).
const (
	readOnlyMaxConns         = 8
	readOnlyStatementTimeout = 5 * time.Second
)

// NewReadOnlyPool opens the api role's pool on dsn. Every session is
// read-only with a 5s statement timeout, set in AfterConnect, so a
// write is refused even if the role's grants are wrong; APIReader also
// runs each read in a read-only transaction.
func NewReadOnlyPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse read-only DSN: %w", err)
	}

	cfg.MaxConns = readOnlyMaxConns
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, stmt := range []string{
			"SET default_transaction_read_only = on",
			fmt.Sprintf("SET statement_timeout = %d", readOnlyStatementTimeout.Milliseconds()),
		} {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("read-only session: %w", err)
			}
		}

		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open read-only pool: %w", err)
	}

	return pool, nil
}
