package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// postureExportInterval is how often the exporter re-reads posture.
const postureExportInterval = time.Minute

// The controls_status buckets (DESIGN-0030 AR-0030-05).
const (
	bucketCompliant     = "compliant"
	bucketNonCompliant  = "non_compliant"
	bucketPending       = "pending"
	bucketStale         = "stale"
	bucketUnmeasurable  = "unmeasurable"
	bucketNotApplicable = "not_applicable"
	bucketUnknown       = "unknown"
)

// PostureReader is the controls compliance query (IMPL-0029 task 4.7).
// *postgres.ControlsStore satisfies it.
type PostureReader interface {
	ControlCompliance(ctx context.Context, orgs []string, lineage string) ([]store.ControlPosture, error)
}

// PostureExporter publishes controls_status and controls_coverage from
// the compliance query. Every api replica runs one (IMPL-0029 OQ8): they
// read the same rows, so dashboards collapse replicas with max by.
type PostureExporter struct {
	reader PostureReader
	logger *slog.Logger
}

// NewPostureExporter returns an exporter over reader. A nil reader is an
// api role not configured to export: Run returns at once and nothing is
// published.
func NewPostureExporter(reader PostureReader, logger *slog.Logger) *PostureExporter {
	return &PostureExporter{reader: reader, logger: logger}
}

// Run exports on a ticker until ctx is done.
func (p *PostureExporter) Run(ctx context.Context) {
	if p.reader == nil {
		return
	}

	t := time.NewTicker(postureExportInterval)
	defer t.Stop()

	for {
		if err := p.Export(ctx); err != nil && ctx.Err() == nil {
			p.logger.Warn("controls posture export failed; keeping the previous values", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// postureKey is one (org, control) series pair.
type postureKey struct{ org, control string }

// Export reads posture, then resets and republishes the gauges. The read
// comes first: resetting first would blank every series for the length
// of the query, and a scrape in that window reads as a vanished fleet.
// On a read error the previous values stay: last known truth beats a
// confident zero. The reset drops the series of an org or control the
// query no longer returns.
func (p *PostureExporter) Export(ctx context.Context) error {
	if p.reader == nil {
		return nil
	}

	rows, err := p.reader.ControlCompliance(ctx, nil, store.LineageAll)
	if err != nil {
		return fmt.Errorf("read controls posture: %w", err)
	}

	// A control can come back once per decision; the gauges are per
	// (org, control).
	sums := make(map[postureKey]*store.ControlPosture)
	order := make([]postureKey, 0, len(rows))

	for i := range rows {
		r := &rows[i]
		k := postureKey{org: r.Org, control: r.ControlID}

		s, ok := sums[k]
		if !ok {
			s = &store.ControlPosture{}
			sums[k] = s
			order = append(order, k)
		}

		addPosture(s, r)
	}

	metrics.ControlsStatus.Reset()
	metrics.ControlsCoverage.Reset()

	for _, k := range order {
		s := sums[k]
		for bucket, n := range map[string]int{
			bucketCompliant:     s.Compliant,
			bucketNonCompliant:  s.NonCompliant,
			bucketPending:       s.Pending,
			bucketStale:         s.Stale,
			bucketUnmeasurable:  s.UnmeasurableAccessDenied + s.UnmeasurableSuspended + s.UnmeasurableConflict,
			bucketNotApplicable: s.NotApplicable,
			bucketUnknown:       s.Unknown,
		} {
			metrics.ControlsStatus.WithLabelValues(k.org, k.control, bucket).Set(float64(n))
		}

		metrics.ControlsCoverage.WithLabelValues(k.org, k.control).Set(float64(s.Assigned))
	}

	return nil
}

func addPosture(dst, src *store.ControlPosture) {
	dst.Compliant += src.Compliant
	dst.NonCompliant += src.NonCompliant
	dst.Pending += src.Pending
	dst.Stale += src.Stale
	dst.UnmeasurableAccessDenied += src.UnmeasurableAccessDenied
	dst.UnmeasurableSuspended += src.UnmeasurableSuspended
	dst.UnmeasurableConflict += src.UnmeasurableConflict
	dst.NotApplicable += src.NotApplicable
	dst.Unknown += src.Unknown
	dst.Assigned += src.Assigned
}
