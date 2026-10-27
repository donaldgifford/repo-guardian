package api

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// fakePosture returns rows, or err, and records whether the gauges were
// already reset when it was read.
type fakePosture struct {
	rows  []store.ControlPosture
	err   error
	prs   []store.OpenPRAge
	holds []store.HoldAge

	sawSeries int
}

func (f *fakePosture) ControlCompliance(context.Context, []string, string) ([]store.ControlPosture, error) {
	f.sawSeries = testutil.CollectAndCount(metrics.ControlsStatus)

	return f.rows, f.err
}

func (f *fakePosture) OpenRemediationPRs(context.Context) ([]store.OpenPRAge, error) {
	return f.prs, nil
}

func (f *fakePosture) RemediationHolds(context.Context) ([]store.HoldAge, error) {
	return f.holds, nil
}

// posture is one codeowners row of org.
func posture(org string, compliant, nonCompliant int) store.ControlPosture {
	return store.ControlPosture{
		Org: org, ControlID: "codeowners", Decision: "baseline",
		Compliant: compliant, NonCompliant: nonCompliant, Assigned: compliant + nonCompliant,
	}
}

// The tests share the package-level gauges, so they run serially.

func TestPostureExporter_PublishesBucketsAndCoverage(t *testing.T) {
	r := &fakePosture{rows: []store.ControlPosture{
		posture("acme", 3, 1),
		{Org: "acme", ControlID: "codeowners", Decision: "org_added", Pending: 2, UnmeasurableAccessDenied: 1, UnmeasurableConflict: 1, Assigned: 4},
	}}

	if err := NewPostureExporter(r, slog.New(slog.DiscardHandler)).Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	for bucket, want := range map[string]float64{bucketCompliant: 3, bucketNonCompliant: 1, bucketPending: 2, bucketUnmeasurable: 2, bucketStale: 0} {
		if got := testutil.ToFloat64(metrics.ControlsStatus.WithLabelValues("acme", "codeowners", bucket)); got != want {
			t.Errorf("controls_status{bucket=%s} = %v, want %v", bucket, got, want)
		}
	}

	if got := testutil.ToFloat64(metrics.ControlsCoverage.WithLabelValues("acme", "codeowners")); got != 8 {
		t.Errorf("controls_coverage = %v, want both decisions summed (8)", got)
	}
}

func TestPostureExporter_ReadsBeforeReset(t *testing.T) {
	r := &fakePosture{rows: []store.ControlPosture{posture("acme", 1, 0)}}
	e := NewPostureExporter(r, slog.New(slog.DiscardHandler))

	for range 2 {
		if err := e.Export(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	if r.sawSeries == 0 {
		t.Error("the gauges were empty while the second read ran; reset must follow the read")
	}
}

func TestPostureExporter_ReadErrorKeepsPreviousValues(t *testing.T) {
	r := &fakePosture{rows: []store.ControlPosture{posture("acme", 5, 0)}}
	e := NewPostureExporter(r, slog.New(slog.DiscardHandler))

	if err := e.Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	r.err = errors.New("connection refused")
	if err := e.Export(t.Context()); err == nil {
		t.Fatal("Export() = nil on a read error, want the error")
	}

	if got := testutil.ToFloat64(metrics.ControlsStatus.WithLabelValues("acme", "codeowners", bucketCompliant)); got != 5 {
		t.Errorf("controls_status after a failed read = %v, want the previous 5", got)
	}
}

func TestPostureExporter_VanishedOrgDropsItsSeries(t *testing.T) {
	r := &fakePosture{rows: []store.ControlPosture{posture("acme", 1, 0), posture("globex", 2, 0)}}
	e := NewPostureExporter(r, slog.New(slog.DiscardHandler))

	if err := e.Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	r.rows = r.rows[:1]
	if err := e.Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(metrics.ControlsCoverage); n != 1 {
		t.Errorf("controls_coverage series = %d, want only acme's after globex vanished", n)
	}
}

func TestPostureExporter_NotConfiguredPublishesNothing(t *testing.T) {
	metrics.ControlsStatus.Reset()
	metrics.ControlsCoverage.Reset()

	e := NewPostureExporter(nil, slog.New(slog.DiscardHandler))
	e.Run(t.Context()) // returns at once

	if err := e.Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(metrics.ControlsStatus) + testutil.CollectAndCount(metrics.ControlsCoverage); n != 0 {
		t.Errorf("series published without a reader = %d, want 0", n)
	}
}

// TestPostureExporter_PublishesOpenPRs is IMPL-0030 7.1:
// remediation_prs_open per org, control and age bucket, with no
// repository or pull request label.
func TestPostureExporter_PublishesOpenPRs(t *testing.T) {
	r := &fakePosture{prs: []store.OpenPRAge{
		{Org: "acme", ControlID: "codeowners", AgeBucket: "<1d", Open: 2},
		{Org: "acme", ControlID: "codeowners", AgeBucket: "30d+", Open: 1},
	}}

	if err := NewPostureExporter(r, slog.New(slog.DiscardHandler)).Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := testutil.ToFloat64(metrics.RemediationPRsOpen.WithLabelValues("acme", "codeowners", "30d+")); got != 1 {
		t.Errorf("remediation_prs_open{age_bucket=30d+} = %v, want 1", got)
	}

	if n := testutil.CollectAndCount(metrics.RemediationPRsOpen); n != 2 {
		t.Errorf("remediation_prs_open has %d series, want 2", n)
	}
}

// TestPostureExporter_PublishesHolds is IMPL-0030 7.2: remediation_holds
// per org, reason and age bucket, the series the conflict-held-a-week
// alert reads.
func TestPostureExporter_PublishesHolds(t *testing.T) {
	r := &fakePosture{holds: []store.HoldAge{
		{Org: "acme", Reason: "conflict", AgeBucket: "7-30d", Held: 3},
		{Org: "acme", Reason: "pr_cap", AgeBucket: "<1d", Held: 1},
	}}

	if err := NewPostureExporter(r, slog.New(slog.DiscardHandler)).Export(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := testutil.ToFloat64(metrics.RemediationHolds.WithLabelValues("acme", "conflict", "7-30d")); got != 3 {
		t.Errorf("remediation_holds{reason=conflict,age_bucket=7-30d} = %v, want 3", got)
	}

	if n := testutil.CollectAndCount(metrics.RemediationHolds); n != 2 {
		t.Errorf("remediation_holds has %d series, want 2", n)
	}
}
