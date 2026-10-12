package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The WaitReady backoff: 1s doubling to 30s, each ping bounded so a
// dropped SYN cannot eat the whole budget.
const (
	waitFirstBackoff = time.Second
	waitMaxBackoff   = 30 * time.Second
	waitPingTimeout  = 10 * time.Second
)

// sqlStateCannotConnectNow is Postgres refusing connections while it
// starts up, recovers or shuts down.
const sqlStateCannotConnectNow = "57P03"

// WaitReady pings db until it answers or wait runs out (IMPL-0028 8.2).
// The migrate Job can start before the database accepts connections:
// Argo CD can report a CNPG Cluster healthy first, and a baked Postgres
// may still be starting. Only "not up yet" errors are retried (a refused
// or timed-out dial, a DNS miss, Postgres's 57P03); anything else, such
// as a rejected password, fails at once. wait <= 0 pings once.
func WaitReady(ctx context.Context, db *sql.DB, wait time.Duration, logger *slog.Logger) error {
	return waitReady(ctx, db.PingContext, wait, waitFirstBackoff, logger)
}

func waitReady(ctx context.Context, ping func(context.Context) error, wait, backoff time.Duration, logger *slog.Logger) error {
	deadline := time.Now().Add(wait)

	for attempt := 1; ; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, waitPingTimeout)
		err := ping(pctx)
		cancel()

		if err == nil {
			if attempt > 1 {
				logger.Info("database is accepting connections", "attempts", attempt)
			}

			return nil
		}

		if !notReady(err) {
			return fmt.Errorf("connecting to the database: %w", err)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("database not accepting connections after %s (%d attempts): %w", wait, attempt, err)
		}

		sleep := min(backoff, remaining)
		logger.Info("waiting for the database", "attempt", attempt, "retry_in", sleep.String(), "error", err.Error())

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the database: %w", errors.Join(ctx.Err(), err))
		case <-time.After(sleep):
		}

		backoff = min(2*backoff, waitMaxBackoff)
	}
}

// notReady reports whether err means the database is not up yet, as
// opposed to up and refusing us.
func notReady(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == sqlStateCannotConnectNow
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error

	return errors.As(err, &netErr) && netErr.Timeout()
}
