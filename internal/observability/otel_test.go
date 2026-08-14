package observability_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/donaldgifford/repo-guardian/internal/observability"
)

// scrape renders reg through the same handler main.go serves and
// returns the exposition text.
func scrape(t *testing.T, reg *promclient.Registry) string {
	t.Helper()

	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL) //nolint:noctx // test-local scrape of a local httptest server
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(body)
}

// newProvider builds a provider against a private registry.
func newProvider(t *testing.T, reg *promclient.Registry) *observability.Provider {
	t.Helper()

	p, err := observability.New(observability.Options{
		Logger:     slog.Default(),
		Registerer: reg,
		Version:    "1.2.3",
	})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}

	return p
}

// TestNew_BridgeAndDomainMetricsShareOneEndpoint is the load-bearing
// claim of the whole OTEL phase (DESIGN-0022): semconv series reach
// Prometheus through the endpoint promhttp already serves, with no
// collector and no scrape-config change.
//
// If the bridge needed its own registry or its own endpoint, every
// operator would need a second scrape target and the "zero new infra"
// premise would be false. This asserts both families come back from a
// single scrape of a single registry.
func TestNew_BridgeAndDomainMetricsShareOneEndpoint(t *testing.T) {
	reg := promclient.NewRegistry()

	domain := promauto.With(reg).NewCounterVec(promclient.CounterOpts{
		Name: "repo_guardian_repos_checked_total",
		Help: "Total repositories processed.",
	}, []string{"org"})
	domain.WithLabelValues("acme").Inc()

	provider := newProvider(t, reg)

	if !provider.Enabled() {
		t.Fatal("Enabled() = false with OTEL_SDK_DISABLED unset, want true")
	}

	counter, err := provider.MeterProvider.Meter("test/scope").Int64Counter("http.server.requests")
	if err != nil {
		t.Fatalf("Int64Counter() = %v, want nil", err)
	}

	counter.Add(context.Background(), 1)

	// Gather() before the substring checks. The bridge registers as an
	// UNCHECKED collector — its Describe is a deliberate no-op — so
	// Prometheus performs no descriptor collision check at registration
	// and a clashing metric name produces no error and no panic when it
	// is registered. It surfaces at scrape time instead, and
	// promhttp.HandlerOpts{} defaults to HTTPErrorOnError: one bad
	// series 500s the ENTIRE endpoint, taking every repo_guardian_
	// metric down with it. A substring assertion alone would not notice,
	// because it would be reading an error page.
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("Gather() = %v, want nil; a collision here 500s the whole /metrics endpoint", err)
	}

	body := scrape(t, reg)

	for _, want := range []string{
		`repo_guardian_repos_checked_total{org="acme"} 1`,
		"http_server_requests_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape is missing %q; the bridge and the domain metrics are not sharing a registry\n%s", want, body)
		}
	}
}

// TestNew_OmitsTargetInfo pins the one bridge option this phase sets.
//
// target_info restates resource attributes that are static per pod,
// while Prometheus already attaches job and instance from the scrape
// config. Left on, it is a series every dashboard author eventually
// has to explain.
func TestNew_OmitsTargetInfo(t *testing.T) {
	reg := promclient.NewRegistry()

	provider := newProvider(t, reg)

	counter, err := provider.MeterProvider.Meter("test/scope").Int64Counter("anything")
	if err != nil {
		t.Fatalf("Int64Counter() = %v, want nil", err)
	}

	counter.Add(context.Background(), 1)

	if body := scrape(t, reg); strings.Contains(body, "target_info") {
		t.Errorf("scrape contains target_info despite WithoutTargetInfo()\n%s", body)
	}
}

// TestNew_DisabledRegistersNothing covers OTEL_SDK_DISABLED.
//
// The Go SDK does not read this variable — no module in the graph
// mentions it — so an operator setting it is relying entirely on this
// package. "Disabled" has to mean the exporter never registers, not
// merely that the numbers are boring: a registered collector still
// costs a scrape and still publishes series.
//
// Non-vacuity: drop the disabled branch from New and the emptiness
// assertion fails.
func TestNew_DisabledRegistersNothing(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	reg := promclient.NewRegistry()

	provider, err := observability.New(observability.Options{Logger: slog.Default(), Registerer: reg})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}

	if provider.Enabled() {
		t.Error("Enabled() = true with OTEL_SDK_DISABLED=true, want false")
	}

	// Recording through the no-op provider must be safe, not a nil
	// dereference: instrumentation wraps unconditionally in this state.
	counter, err := provider.MeterProvider.Meter("test/scope").Int64Counter("http.server.requests")
	if err != nil {
		t.Fatalf("Int64Counter() = %v, want nil", err)
	}

	counter.Add(context.Background(), 1)

	if body := scrape(t, reg); strings.TrimSpace(body) != "" {
		t.Errorf("disabled SDK still published series:\n%s", body)
	}

	// Shutdown is a no-op rather than nil so main.go needs no branch.
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() on a disabled provider = %v, want nil", err)
	}
}

// TestNew_DisabledParsing covers the accepted spellings and the
// fallback.
//
// An unparseable value warns and leaves telemetry ENABLED, per the
// OpenTelemetry environment-variable specification. Failing startup
// would trade a metrics problem for an outage, which is a bad deal for
// a telemetry switch; and the fallback direction is the safe one,
// since a typo leaves the deployment observable and complaining in the
// log rather than silently blind.
func TestNew_DisabledParsing(t *testing.T) {
	for _, tc := range []struct {
		value       string
		wantEnabled bool
	}{
		{value: "true", wantEnabled: false},
		{value: "1", wantEnabled: false},
		{value: "TRUE", wantEnabled: false},
		{value: "false", wantEnabled: true},
		{value: "0", wantEnabled: true},
		{value: "", wantEnabled: true},
		// Unparseable: warn, stay enabled, do not take the pod down.
		{value: "yes", wantEnabled: true},
		{value: "disabled", wantEnabled: true},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("OTEL_SDK_DISABLED", tc.value)

			provider, err := observability.New(observability.Options{
				Logger:     slog.Default(),
				Registerer: promclient.NewRegistry(),
			})
			if err != nil {
				t.Fatalf("New() = %v, want nil; a telemetry env var must never fail startup", err)
			}

			if got := provider.Enabled(); got != tc.wantEnabled {
				t.Errorf("Enabled() = %v, want %v", got, tc.wantEnabled)
			}
		})
	}
}

// TestNew_RequiresLogger pins the one required option. A nil logger
// would panic on the first Info call, which is a worse startup failure
// than a named error.
func TestNew_RequiresLogger(t *testing.T) {
	if _, err := observability.New(observability.Options{Registerer: promclient.NewRegistry()}); err == nil {
		t.Error("New() without a logger = nil error, want an error")
	}
}

// TestNew_ShutdownIsCleanWhenEnabled exercises the lifecycle main.go
// depends on during graceful shutdown.
func TestNew_ShutdownIsCleanWhenEnabled(t *testing.T) {
	provider := newProvider(t, promclient.NewRegistry())

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}
