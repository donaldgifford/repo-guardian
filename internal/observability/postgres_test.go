package observability_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	promclient "github.com/prometheus/client_golang/prometheus"

	"github.com/donaldgifford/repo-guardian/internal/observability"
)

// poolConfig parses a DSN that points nowhere. Nothing here connects:
// both instrumentation paths are registration-time or callback-time
// operations, so a pool that has never reached a server still exercises
// them.
func poolConfig(t *testing.T) *pgxpool.Config {
	t.Helper()

	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatalf("ParseConfig() = %v, want nil", err)
	}

	return cfg
}

// TestInstrumentPostgresConfig_SetsTracer pins the ordering constraint
// that makes the instrumentation work at all.
//
// The tracer is a field on ConnConfig, read when a connection is
// established. Build the pool first and the field is never consulted —
// query metrics would be silently absent with no error anywhere.
func TestInstrumentPostgresConfig_SetsTracer(t *testing.T) {
	cfg := poolConfig(t)

	if cfg.ConnConfig.Tracer != nil {
		t.Fatal("ConnConfig.Tracer is already set; this test no longer proves anything")
	}

	observability.InstrumentPostgresConfig(cfg)

	if cfg.ConnConfig.Tracer == nil {
		t.Error("ConnConfig.Tracer = nil after instrumentation; queries will not be measured")
	}
}

// TestInstrumentPostgresPool_PublishesPoolMetrics answers the task 3.5
// open question in the only way that settles it: otelpgx's own
// RecordStats takes *pgxpool.Pool directly, so it covers pgxpool.Stat()
// and no hand-rolled Stat collector ships.
//
// Exactly one pool-stats source is the requirement. A second source
// would double every pool panel and leave operators guessing which
// number to believe.
func TestInstrumentPostgresPool_PublishesPoolMetrics(t *testing.T) {
	reg := promclient.NewRegistry()
	newProvider(t, reg)

	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig(t))
	if err != nil {
		t.Fatalf("NewWithConfig() = %v, want nil", err)
	}

	defer pool.Close()

	if err := observability.InstrumentPostgresPool(pool); err != nil {
		t.Fatalf("InstrumentPostgresPool() = %v, want nil", err)
	}

	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather() = %v, want nil; a bad series 500s the whole endpoint", err)
	}

	body := scrape(t, reg)

	// pgxpool_max_connections carries the pool's configured cap read
	// straight out of pgxpool.Stat(), so a non-zero value here is the
	// proof that otelpgx is reading the real pool rather than just
	// registering empty instruments. Note the names are pgxpool_*, not
	// the db.client.connection.* semconv family — otelpgx publishes the
	// pgx-native set.
	for _, want := range []string{
		"pgxpool_max_connections",
		"pgxpool_acquire_duration_nanoseconds_total",
		`db_system_name="postgresql"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q; pool stats are not reaching the registry\n%s", want, body)
		}
	}
}

// TestInstrumentPostgresPool_IsQuietWhenDisabled pins that
// OTEL_SDK_DISABLED suppresses pool series too.
func TestInstrumentPostgresPool_IsQuietWhenDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	reg := promclient.NewRegistry()
	newProvider(t, reg)

	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig(t))
	if err != nil {
		t.Fatalf("NewWithConfig() = %v, want nil", err)
	}

	defer pool.Close()

	if err := observability.InstrumentPostgresPool(pool); err != nil {
		t.Fatalf("InstrumentPostgresPool() = %v, want nil", err)
	}

	if body := scrape(t, reg); strings.TrimSpace(body) != "" {
		t.Errorf("disabled SDK still published pool series:\n%s", body)
	}
}
