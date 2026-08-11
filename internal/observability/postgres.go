package observability

import (
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InstrumentPostgresConfig attaches query instrumentation to a pgxpool
// config. Call before the pool is built.
//
// This measures what the domain metrics structurally cannot. The
// existing store_query_seconds{op} is timed around whole store methods,
// so it reports how long "StaleRepos" took as one number. The semconv
// operation duration separates the parts of that number: time spent
// waiting for a connection from the pool is not time spent executing
// SQL, and only one of those is fixed by tuning the query.
//
// The two are complementary, not redundant, and the DESIGN-0022 dedup
// rule applies: store_query_seconds stays authoritative for "which
// store operation is slow" — it knows the difference between StaleRepos
// and UpsertIfMissing, where semconv sees only SQL verbs. Semconv is
// authoritative for "why".
func InstrumentPostgresConfig(cfg *pgxpool.Config) {
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
}

// InstrumentPostgresPool publishes connection-pool statistics.
//
// This resolves the IMPL-0023 task 3.5 open question — whether otelpgx
// covers pgxpool.Stat() or whether a hand-rolled Stat collector is also
// needed. It covers it: otelpgx.RecordStats takes an interface that
// *pgxpool.Pool already satisfies (Stat() plus Config()). Exactly one
// pool-stats source ships, and it is this one.
//
// Registration is an asynchronous callback rather than a goroutine; the
// pool is only read when the registry is scraped, subject to a minimum
// read interval.
func InstrumentPostgresPool(pool *pgxpool.Pool) error {
	if err := otelpgx.RecordStats(pool); err != nil {
		return fmt.Errorf("observability: instrument postgres pool: %w", err)
	}

	return nil
}
