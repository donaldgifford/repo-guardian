package observability_test

import (
	"strings"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/donaldgifford/repo-guardian/internal/observability"
)

// TestInstrumentValkey_PublishesPoolMetrics covers the wiring for
// IMPL-0023 task 3.4.
//
// No server is needed and that is the point: the connection-pool
// instruments are asynchronous observables, read from the client's own
// pool stats at collection time. A client that cannot reach Valkey
// still reports its pool — which is exactly the state an operator most
// wants a number for.
//
// Command latency needs a real server and is covered by the queue's
// integration tests, not here.
func TestInstrumentValkey_PublishesPoolMetrics(t *testing.T) {
	reg := promclient.NewRegistry()
	newProvider(t, reg)

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = client.Close() }()

	if err := observability.InstrumentValkey(client); err != nil {
		t.Fatalf("InstrumentValkey() = %v, want nil", err)
	}

	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather() = %v, want nil; a bad series 500s the whole endpoint", err)
	}

	body := scrape(t, reg)

	// db.client.connections.usage is the semconv pool gauge; its
	// presence is what proves the callbacks registered against our
	// provider rather than a no-op one.
	if want := "db_client_connections_usage"; !strings.Contains(body, want) {
		t.Errorf("scrape is missing %q; Valkey pool metrics are not reaching the registry\n%s", want, body)
	}

	// pool.name keeps the series attributable when a second client is
	// ever added; it is the client address, so it is bounded.
	if want := `pool_name="127.0.0.1:1"`; !strings.Contains(body, want) {
		t.Errorf("scrape is missing %q\n%s", want, body)
	}
}

// TestInstrumentValkey_IsQuietWhenDisabled pins that OTEL_SDK_DISABLED
// suppresses Valkey series too.
//
// redisotel registers asynchronous callbacks against whatever provider
// is global at call time, so this is really a check that the bootstrap
// ordering in main.go holds: instrument after New, and a disabled SDK
// stays silent.
func TestInstrumentValkey_IsQuietWhenDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	reg := promclient.NewRegistry()
	newProvider(t, reg)

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = client.Close() }()

	if err := observability.InstrumentValkey(client); err != nil {
		t.Fatalf("InstrumentValkey() = %v, want nil", err)
	}

	if body := scrape(t, reg); strings.TrimSpace(body) != "" {
		t.Errorf("disabled SDK still published Valkey series:\n%s", body)
	}
}
