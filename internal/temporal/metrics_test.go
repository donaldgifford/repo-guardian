package temporal

import (
	"slices"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetricViews_SecondScaleBuckets(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(MetricViews()...))

	sdk, err := mp.Meter(SDKMeterName).Float64Histogram("temporal_workflow_task_schedule_to_start_latency")
	if err != nil {
		t.Fatal(err)
	}

	other, err := mp.Meter("other").Float64Histogram("other_latency")
	if err != nil {
		t.Fatal(err)
	}

	sdk.Record(t.Context(), 0.15)
	other.Record(t.Context(), 0.15)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}

	bounds := map[string][]float64{}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if h, ok := m.Data.(metricdata.Histogram[float64]); ok {
				bounds[sm.Scope.Name] = h.DataPoints[0].Bounds
			}
		}
	}

	if got := bounds[SDKMeterName]; !slices.Equal(got, latencyBuckets) {
		t.Errorf("SDK histogram bounds = %v, want %v", got, latencyBuckets)
	}

	if !slices.Contains(bounds[SDKMeterName], 0.2) {
		t.Error("no 0.2 edge: the 200ms schedule-to-start alert would be interpolated")
	}

	// Scoped to the SDK: other instruments keep their own boundaries.
	if slices.Equal(bounds["other"], latencyBuckets) {
		t.Error("the view leaked onto another meter's histogram")
	}
}
