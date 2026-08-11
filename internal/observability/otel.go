// Package observability bootstraps the OpenTelemetry SDK.
//
// The strategy is metrics-first (DESIGN-0022 §OTEL metrics-first
// instrumentation): four transport boundaries get off-the-shelf
// instrumentation — otelhttp inbound and outbound, redisotel, otelpgx —
// and their measurements reach Prometheus through this package's
// bridge. No collector is deployed, no new endpoint is served, and the
// scrape configuration does not change.
//
// That is the whole point of the design: the alternative was four
// hand-rolled collectors, each duplicating instrumentation that already
// exists upstream and each needing its own tests.
//
// # Metrics only
//
// The TracerProvider is the no-op provider. Instrumentation libraries
// still create spans structurally — they have no metrics-only mode —
// but those spans are discarded rather than exported. Turning tracing
// on is a later design's job; wiring an exporter here would commit us
// to a sampling policy and a collector we have not chosen.
//
// # Relationship to internal/metrics
//
// Both land in the same Prometheus registry, and they answer different
// questions. Domain metrics stay authoritative for domain questions:
// store_query_seconds{op} knows the difference between StaleRepos and
// UpsertIfMissing, while the semconv database metrics see only SQL
// verbs. The dedup rule from DESIGN-0022 is that a dashboard panel
// picks exactly one source and never mixes a hand-rolled series with a
// semconv one for the same signal.
package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// disabledEnv is the SDK-wide off switch from the OpenTelemetry
// environment-variable specification.
//
// It is spelled out here because the Go SDK does not read it. Other
// language SDKs honour it internally; opentelemetry-go does not, and
// nothing in the module graph does either — verified by grepping every
// vendored go.opentelemetry.io module for the string, which matches
// nothing. An operator who sets it and assumes it took effect would
// otherwise be wrong, so this package implements it.
const disabledEnv = "OTEL_SDK_DISABLED"

// serviceName is the value reported as service.name.
const serviceName = "repo-guardian"

// Options configures New.
type Options struct {
	// Logger receives one line describing what was wired. Required.
	Logger *slog.Logger

	// Registerer is where the bridge registers its collector. Nil means
	// prometheus.DefaultRegisterer — the registry promauto writes to and
	// promhttp.Handler serves, which is what puts semconv series on the
	// existing /metrics endpoint.
	//
	// Tests pass a private registry so they can read the output without
	// the rest of the binary's metrics in it.
	Registerer promclient.Registerer

	// Version is reported as service.version. Empty is fine; the
	// attribute is simply omitted rather than reporting a placeholder.
	Version string
}

// Provider is the bootstrapped SDK and its shutdown hook.
type Provider struct {
	// MeterProvider is the provider that was also installed globally.
	// Instrumentation that takes an explicit provider should be given
	// this; instrumentation that defaults to otel.GetMeterProvider()
	// picks it up on its own.
	MeterProvider otelmetric.MeterProvider

	shutdown func(context.Context) error
	enabled  bool
}

// New bootstraps the SDK and installs it globally.
//
// Installing globally is the point rather than a side effect: otelhttp,
// redisotel and otelpgx all default to otel.GetMeterProvider() at
// construction time, so a global set before they are built means none
// of the four call sites needs to know whether telemetry is on.
//
// When OTEL_SDK_DISABLED is truthy the globals are set to the no-op
// providers and no exporter is registered. Instrumentation still wraps
// unconditionally in that state, which is deliberate: the alternative
// is an `if enabled` branch at four call sites in four packages, and
// the cost of being wrong there — silently unwrapping a transport
// nobody notices is unmeasured — is much higher than the cost of a
// no-op meter, which resolves to an interface call returning a discard
// instrument. Shutdown is a no-op in this state, not nil, so callers
// need no branch either.
func New(opts Options) (*Provider, error) {
	if opts.Logger == nil {
		return nil, errors.New("observability: logger is required")
	}

	// The TracerProvider is no-op in both branches. Instrumentation
	// libraries have no metrics-only switch, so spans get created and
	// dropped; this makes the drop explicit rather than leaving the
	// global as whatever otel defaults to.
	otel.SetTracerProvider(tracenoop.NewTracerProvider())

	disabled := sdkDisabled(opts.Logger)

	if disabled {
		provider := metricnoop.NewMeterProvider()
		otel.SetMeterProvider(provider)

		opts.Logger.Info("opentelemetry disabled", "reason", disabledEnv)

		return &Provider{
			MeterProvider: provider,
			shutdown:      func(context.Context) error { return nil },
		}, nil
	}

	registerer := opts.Registerer
	if registerer == nil {
		registerer = promclient.DefaultRegisterer
	}

	exporter, err := promexporter.New(
		promexporter.WithRegisterer(registerer),

		// target_info is a single series carrying the resource
		// attributes. Prometheus already attaches job and instance from
		// the scrape config, and the remaining attributes are static for
		// the lifetime of a pod, so it buys nothing here and costs a
		// series plus a join for anyone who trips over it.
		promexporter.WithoutTargetInfo(),

		// Scope labels are dropped because otel_scope_version is the
		// INSTRUMENTATION LIBRARY's version, stamped on every series that
		// library produces. Every renovate bump of otelhttp, redisotel or
		// otelpgx would therefore change a label value on every one of
		// their series: the old series goes stale, a new one starts at
		// zero, and rate()/increase() across the deploy sees a counter
		// reset. Phase 6 bakes PromQL into generated dashboards with a
		// fail-on-diff gate, so a label that churns on dependency updates
		// is a recurring breakage, not a cosmetic one.
		//
		// Safe because nothing needs them as a discriminator: the four
		// instrumentations emit disjoint metric names (http.server.* vs
		// http.client.* vs db.client.connections.*/redis.* vs
		// db.client.operation.*/pgxpool.*), so the metric name already
		// identifies the producer. Revisit if two scopes ever emit the
		// same name.
		promexporter.WithoutScopeInfo(),

		// Pin the name-translation strategy instead of inheriting it.
		// The exporter's default is documented as depending on
		// prometheus/common's NameValidationScheme and as subject to
		// change in a future release — and this repo now pins a
		// prometheus/common whose scheme is already UTF8Validation. If
		// that default ever flips, every metric name loses its
		// underscore escaping and its _total/_seconds suffixes at once,
		// which would break every generated dashboard panel, every alert
		// in prometheusrule.yaml, and the repo's own naming convention
		// in a single dependency bump. Being explicit costs one import.
		promexporter.WithTranslationStrategy(otlptranslator.UnderscoreEscapingWithSuffixes),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: prometheus exporter: %w", err)
	}

	res, err := newResource(opts.Version)
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)

	otel.SetMeterProvider(provider)

	opts.Logger.Info("opentelemetry enabled",
		"signals", "metrics",
		"exporter", "prometheus",
		"traces", "noop",
	)

	return &Provider{
		MeterProvider: provider,
		shutdown:      provider.Shutdown,
		enabled:       true,
	}, nil
}

// Enabled reports whether real metrics are being collected.
//
// Call sites should NOT branch on this to decide whether to instrument
// — see New. It exists for logging and for tests that need to assert
// which branch was taken.
func (p *Provider) Enabled() bool { return p.enabled }

// Shutdown flushes and releases the SDK. Safe on a disabled provider.
//
// There is nothing to flush in the Prometheus setup — the exporter is a
// pull-based Reader, so the last scrape has already happened or never
// will — but calling it keeps the lifecycle honest if a push exporter
// is ever added alongside.
func (p *Provider) Shutdown(ctx context.Context) error {
	if err := p.shutdown(ctx); err != nil {
		return fmt.Errorf("observability: shutdown: %w", err)
	}

	return nil
}

// newResource builds the resource describing this process.
//
// The attributes are deliberately SCHEMALESS. resource.Merge refuses to
// merge two resources carrying different schema URLs, and
// resource.Default() carries whichever schema the SDK release was built
// against — 1.43.0 today, which happens to match the semconv package
// imported here. Nothing keeps those two in step: the next SDK bump
// that moves Default() forward would make Merge return "conflicting
// Schema URL", New return an error, and main.go abort startup. The
// symptom is a crash-looping pod after a routine dependency bump, and
// the cause is two version numbers in different modules.
//
// A schemaless resource merges with anything. The cost is that
// service.name and service.version carry no schema URL — attributes
// that have been stable since semconv 1.0 and are not worth a startup
// failure mode.
func newResource(version string) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{semconv.ServiceName(serviceName)}
	if version != "" {
		attrs = append(attrs, semconv.ServiceVersion(version))
	}

	// Merge over resource.Default() rather than replacing it: the
	// default carries telemetry.sdk.*, and Merge's precedence gives our
	// attributes the win on any key collision.
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, fmt.Errorf("observability: resource: %w", err)
	}

	return res, nil
}

// sdkDisabled reads OTEL_SDK_DISABLED.
//
// An unparseable value warns and falls back to the default (enabled),
// per the OpenTelemetry environment-variable specification. Failing
// startup instead would be disproportionate: this is a telemetry
// switch, and taking the whole service down over a malformed value for
// it trades a metrics problem for an outage. Falling back is also not
// the dangerous direction — the default is enabled, so a typo leaves
// the deployment fully observable and shouting about it in the log,
// rather than silently blind.
func sdkDisabled(logger *slog.Logger) bool {
	raw, ok := os.LookupEnv(disabledEnv)
	if !ok || raw == "" {
		return false
	}

	disabled, err := strconv.ParseBool(raw)
	if err != nil {
		logger.Warn("ignoring unparseable "+disabledEnv+"; telemetry stays enabled",
			"value", raw,
			"error", err,
		)

		return false
	}

	return disabled
}
