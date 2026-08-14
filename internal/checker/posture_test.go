package checker_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/donaldgifford/repo-guardian/internal/checker"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// posturePortStore is a file-local recorder per the house convention
// (CLAUDE.md §Test-local recording fakes): it serves a scripted Posture
// and counts reads, which is everything these tests observe.
type posturePortStore struct {
	store.Store

	posture *store.Posture
	err     error
	reads   int
}

func (s *posturePortStore) Posture(context.Context) (*store.Posture, error) {
	s.reads++

	if s.err != nil {
		return nil, s.err
	}

	return s.posture, nil
}

func newPostureExporter(st store.Store) *checker.PostureExporter {
	return checker.NewPostureExporter(checker.PostureExporterOptions{
		Store:  st,
		Logger: slog.Default(),
	})
}

// TestPostureExport_PublishesEveryAggregate is the happy path: each of
// the three slices lands on its own gauge with the right labels.
func TestPostureExport_PublishesEveryAggregate(t *testing.T) {
	// Not parallel: package-global gauges.
	metrics.ResetPosture()

	st := &posturePortStore{posture: &store.Posture{
		Actionable: []store.RuleCount{
			{Org: "acme", RuleName: "codeowners", Count: 7},
			{Org: "acme", RuleName: "dependabot", Count: 2},
			{Org: "globex", RuleName: "codeowners", Count: 1},
		},
		Tracked: []store.OrgCount{
			{Org: "acme", Count: 40},
			{Org: "globex", Count: 9},
		},
		Unmeasurable: []store.ReasonCount{
			{Org: "acme", Reason: store.ReasonAccessDenied, Count: 3},
			{Org: "acme", Reason: store.ReasonArchived, Count: 11},
		},
	}}

	if err := newPostureExporter(st).Export(context.Background()); err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}

	for _, tc := range []struct {
		labels []string
		want   float64
	}{
		{[]string{"codeowners", "acme"}, 7},
		{[]string{"dependabot", "acme"}, 2},
		{[]string{"codeowners", "globex"}, 1},
	} {
		if got := testutil.ToFloat64(metrics.ReposActionable.WithLabelValues(tc.labels...)); got != tc.want {
			t.Errorf("repos_actionable%v = %v, want %v", tc.labels, got, tc.want)
		}
	}

	if got := testutil.ToFloat64(metrics.ReposTracked.WithLabelValues("acme")); got != 40 {
		t.Errorf("repos_tracked{org=acme} = %v, want 40", got)
	}

	if got := testutil.ToFloat64(metrics.ReposUnmeasurable.WithLabelValues("acme", store.ReasonAccessDenied)); got != 3 {
		t.Errorf("repos_unmeasurable{org=acme, reason=access_denied} = %v, want 3", got)
	}

	if got := testutil.ToFloat64(metrics.ReposUnmeasurable.WithLabelValues("acme", store.ReasonArchived)); got != 11 {
		t.Errorf("repos_unmeasurable{org=acme, reason=archived} = %v, want 11", got)
	}
}

// TestPostureExport_StaleSeriesDieOnTheNextTick is the reason the
// exporter resets rather than only setting.
//
// A rule that stops applying, or an org that leaves the fleet, would
// otherwise freeze at its last value forever and keep counting against
// compliance — with nothing to correct it, because the row that would
// have updated the series is exactly the row that no longer exists.
//
// Non-vacuity: drop metrics.ResetPosture() from Export and this fails
// on both the rule and the org.
func TestPostureExport_StaleSeriesDieOnTheNextTick(t *testing.T) {
	metrics.ResetPosture()

	st := &posturePortStore{posture: &store.Posture{
		Actionable: []store.RuleCount{
			{Org: "acme", RuleName: "codeowners", Count: 7},
			{Org: "globex", RuleName: "renovate", Count: 4},
		},
		Tracked: []store.OrgCount{{Org: "acme", Count: 40}, {Org: "globex", Count: 9}},
		Unmeasurable: []store.ReasonCount{
			{Org: "acme", Reason: store.ReasonFork, Count: 5},
		},
	}}

	exporter := newPostureExporter(st)
	if err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("first Export() = %v, want nil", err)
	}

	// globex leaves the fleet entirely; acme keeps one rule and loses
	// the fork parks.
	st.posture = &store.Posture{
		Actionable: []store.RuleCount{{Org: "acme", RuleName: "codeowners", Count: 6}},
		Tracked:    []store.OrgCount{{Org: "acme", Count: 40}},
	}

	if err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("second Export() = %v, want nil", err)
	}

	if got := testutil.ToFloat64(metrics.ReposActionable.WithLabelValues("codeowners", "acme")); got != 6 {
		t.Errorf("surviving series = %v, want 6", got)
	}

	if n := testutil.CollectAndCount(metrics.ReposActionable); n != 1 {
		t.Errorf("repos_actionable series = %d, want 1; a rule that stopped applying is still being reported", n)
	}

	if n := testutil.CollectAndCount(metrics.ReposTracked); n != 1 {
		t.Errorf("repos_tracked series = %d, want 1; an org that left the fleet is still counted", n)
	}

	if n := testutil.CollectAndCount(metrics.ReposUnmeasurable); n != 0 {
		t.Errorf("repos_unmeasurable series = %d, want 0; parks that were resolved still show", n)
	}
}

// TestPostureExport_ReadFailureKeepsLastKnownValues pins the ordering
// inside Export: read first, reset only once the read succeeded.
//
// Resetting first would blank every series for the duration of the
// query, so a scrape landing in that window reports zero repositories
// tracked — indistinguishable from a fleet that vanished, and a
// compliance ratio of 0/0. On a failure the right answer for a gauge
// is last known truth, not a confident zero.
//
// Non-vacuity: move ResetPosture() above the store read and this
// fails.
func TestPostureExport_ReadFailureKeepsLastKnownValues(t *testing.T) {
	metrics.ResetPosture()

	st := &posturePortStore{posture: &store.Posture{
		Actionable: []store.RuleCount{{Org: "acme", RuleName: "codeowners", Count: 7}},
		Tracked:    []store.OrgCount{{Org: "acme", Count: 40}},
	}}

	exporter := newPostureExporter(st)
	if err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("first Export() = %v, want nil", err)
	}

	st.err = errors.New("connection refused")

	if err := exporter.Export(context.Background()); err == nil {
		t.Fatal("Export() = nil on a store failure, want an error so the scheduler can count it")
	}

	if got := testutil.ToFloat64(metrics.ReposActionable.WithLabelValues("codeowners", "acme")); got != 7 {
		t.Errorf("repos_actionable after a failed read = %v, want the previous 7", got)
	}

	if got := testutil.ToFloat64(metrics.ReposTracked.WithLabelValues("acme")); got != 40 {
		t.Errorf("repos_tracked after a failed read = %v, want the previous 40", got)
	}
}

// TestPostureExport_EmptyFleetPublishesNothing covers the freshly
// migrated deployment: rows appear as repos are checked, so before the
// first sweep there is no posture at all. That must read as absent
// series, not as zeros — a zero denominator and an absent denominator
// look different in PromQL, and only one of them is honest.
func TestPostureExport_EmptyFleetPublishesNothing(t *testing.T) {
	metrics.ResetPosture()

	st := &posturePortStore{posture: &store.Posture{}}

	if err := newPostureExporter(st).Export(context.Background()); err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}

	if n := testutil.CollectAndCount(metrics.ReposActionable); n != 0 {
		t.Errorf("repos_actionable series = %d, want 0", n)
	}

	if n := testutil.CollectAndCount(metrics.ReposTracked); n != 0 {
		t.Errorf("repos_tracked series = %d, want 0", n)
	}

	if st.reads != 1 {
		t.Errorf("store reads = %d, want exactly 1 per tick", st.reads)
	}
}

// TestPostureExport_HealthMetrics covers the liveness signal for every
// posture gauge (IMPL-0023 task 2.4).
//
// The gauges have no other heartbeat. A leader whose store reads all
// fail keeps serving its last successful values indefinitely, so from
// a dashboard "the fleet is stable" and "nothing has updated in six
// hours" look identical. The outcome counter is what tells them apart,
// which is why the alert watches it rather than the gauges.
//
// The duration observation is deliberately taken on the failure path
// too: a store read that times out is the most useful sample the
// histogram can hold, and skipping it would leave the p99 looking
// healthy exactly when it is not.
func TestPostureExport_HealthMetrics(t *testing.T) {
	metrics.ResetPosture()
	metrics.PostureExportTotal.Reset()

	st := &posturePortStore{posture: &store.Posture{
		Tracked: []store.OrgCount{{Org: "acme", Count: 3}},
	}}

	exporter := newPostureExporter(st)

	// A plain Histogram has no Reset, and this one is package-global,
	// so the assertion is on the delta rather than the absolute count.
	before := histogramCount(t, metrics.PostureExportDurationSeconds)

	if err := exporter.Export(context.Background()); err != nil {
		t.Fatalf("Export() = %v, want nil", err)
	}

	if got := testutil.ToFloat64(metrics.PostureExportTotal.WithLabelValues("ok")); got != 1 {
		t.Errorf("posture_export_total{outcome=ok} = %v, want 1", got)
	}

	st.err = errors.New("connection refused")

	if err := exporter.Export(context.Background()); err == nil {
		t.Fatal("Export() = nil on a store failure, want an error")
	}

	if got := testutil.ToFloat64(metrics.PostureExportTotal.WithLabelValues("error")); got != 1 {
		t.Errorf("posture_export_total{outcome=error} = %v, want 1", got)
	}

	if got := testutil.ToFloat64(metrics.PostureExportTotal.WithLabelValues("ok")); got != 1 {
		t.Errorf("posture_export_total{outcome=ok} = %v, want 1; a failed tick counted as a success", got)
	}

	// Both ticks observed, the failing one included.
	if got := histogramCount(t, metrics.PostureExportDurationSeconds) - before; got != 2 {
		t.Errorf("duration observations = %d, want 2 (the failed read must be measured too)", got)
	}
}

// histogramCount reads the sample count out of a plain Histogram.
func histogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()

	// Comma-ok rather than a bare assertion: a plain Histogram satisfies
	// prometheus.Metric today, but if this ever gets handed a
	// HistogramVec child or a wrapper that does not, the bare form
	// panics with no indication of what was actually passed.
	metric, ok := h.(prometheus.Metric)
	if !ok {
		t.Fatalf("histogram %T does not implement prometheus.Metric", h)
	}

	var m dto.Metric
	if err := metric.Write(&m); err != nil {
		t.Fatalf("write histogram: %v", err)
	}

	return m.GetHistogram().GetSampleCount()
}
