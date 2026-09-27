package temporal

import (
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// SDKMeterName is the OTel meter the Temporal SDK's metrics are
// recorded on (see Dial), and the scope MetricViews matches.
const SDKMeterName = "temporal-sdk-go"

// latencyBuckets are second-scale boundaries for the SDK's latency
// histograms. The OTel default (0, 5, 10, 25 … 10000) assumes
// milliseconds, but the SDK records seconds, so every latency under 5s
// landed in one bucket and no quantile below that was computable. 0.2
// is an edge so the 200ms schedule-to-start threshold Temporal
// recommends is read, not interpolated.
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// MetricViews returns the views the Temporal SDK's instruments need.
// Pass them to the meter provider handed to Dial.
func MetricViews() []sdkmetric.View {
	return []sdkmetric.View{
		sdkmetric.NewView(
			sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram, Scope: instrumentation.Scope{Name: SDKMeterName}},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: latencyBuckets}},
		),
	}
}
