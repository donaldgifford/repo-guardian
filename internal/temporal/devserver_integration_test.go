//go:build integration

package temporal_test

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

// TestDial_DevServer is the Phase 9 success criterion against the dev
// server: Dial connects, the pinned server passes the version floor, a
// floor above it is refused, and SDK metrics reach the meter provider.
func TestDial_DevServer(t *testing.T) {
	t.Parallel()

	srv := temporaltest.Start(t)
	reader := metric.NewManualReader()

	c, err := temporal.Dial(t.Context(), &srv.Config, temporal.DialOptions{
		MeterProvider: metric.NewMeterProvider(metric.WithReader(reader)),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(c.Close)

	if err := temporal.Ping(t.Context(), c); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	if err := temporal.CheckServerVersion(t.Context(), c, temporal.MinServerVersion); err != nil {
		t.Errorf("CheckServerVersion(%s): %v", temporal.MinServerVersion, err)
	}

	if err := temporal.CheckServerVersion(t.Context(), c, "99.0.0"); !errors.Is(err, temporal.ErrServerTooOld) {
		t.Errorf("CheckServerVersion(99.0.0) = %v, want ErrServerTooOld", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	n := 0
	for _, sm := range rm.ScopeMetrics {
		n += len(sm.Metrics)
	}

	if n == 0 {
		t.Error("no SDK metrics reached the meter provider")
	}
}
