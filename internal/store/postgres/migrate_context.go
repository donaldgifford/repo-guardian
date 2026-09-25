package postgres

import (
	"context"
	"time"
)

// DefaultBackfillFreshness seeds next_due_at when the operator gives no
// freshness: v1's RECONCILE_FRESHNESS default.
const DefaultBackfillFreshness = 24 * time.Hour

type freshnessKey struct{}

// WithBackfillFreshness returns a context carrying the freshness the
// v1 backfill uses to seed repositories.next_due_at. goose Go
// migrations receive only a context and a transaction, so this is how
// the migrate subcommand's --freshness flag reaches them.
func WithBackfillFreshness(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, freshnessKey{}, d)
}

// backfillFreshness returns the freshness carried by ctx, or
// DefaultBackfillFreshness when there is none.
func backfillFreshness(ctx context.Context) time.Duration {
	if d, ok := ctx.Value(freshnessKey{}).(time.Duration); ok && d > 0 {
		return d
	}

	return DefaultBackfillFreshness
}
