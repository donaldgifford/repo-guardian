// Posture export for the Postgres-backed multi-replica deployment.
// See DESIGN-0022 §Leader-scoped posture exporter and IMPL-0023
// Phase 2.
//
// The legacy compliance signal was a set of unlabeled counters
// incremented at check time, which answered "how often did we act"
// while every question actually asked of them was "how many
// repositories are failing right now" (INV-0013 Finding B). A counter
// cannot answer that: it only ever grows, and a repository fixed
// yesterday still shows in its rate.
//
// PostureExporter projects the rule_state table onto gauges instead.
// State lives in Postgres, written by the worker after every check;
// this reads it back on a timer and publishes the current answer.

package checker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Outcome label values for PostureExportTotal.
const (
	outcomeOK    = "ok"
	outcomeError = "error"
)

// PostureExporter publishes fleet compliance posture from the store.
//
// Register Export as a scheduled handler under the same leader election
// as stale-sweep; it is not safe to run on every replica. Not because
// it writes anything — it does not — but because each replica publishes
// its own copy of the series, and a rule's count is only meaningful
// once. Non-leaders publish nothing rather than something stale.
//
// The consequence for anyone querying these gauges: aggregate with
// `max by (...)`, never `sum`. During a failover the outgoing and
// incoming leaders can both hold series briefly, and a demoted replica
// keeps serving whatever it last published until its process restarts.
// `max` is correct in both states; `sum` double-counts through every
// leader change.
type PostureExporter struct {
	store  store.Store
	logger *slog.Logger
}

// PostureExporterOptions bundles the PostureExporter constructor
// inputs.
type PostureExporterOptions struct {
	Store  store.Store
	Logger *slog.Logger
}

// NewPostureExporter builds a PostureExporter.
func NewPostureExporter(opts PostureExporterOptions) *PostureExporter {
	return &PostureExporter{
		store:  opts.Store,
		logger: opts.Logger,
	}
}

// Export runs one posture tick: read the whole aggregate, then
// republish every gauge.
//
// Registered under the same SETNX leader election as stale-sweep, so
// exactly one replica serves these series at a time. Non-leaders do
// not publish stale values; they publish nothing, which is why
// dashboards aggregate with `max by (...)` rather than `sum`.
//
// The read happens BEFORE the reset. Resetting first would blank every
// series for the duration of the query, so a scrape landing in that
// window sees zero repositories tracked — indistinguishable from a
// fleet that vanished, and on a 60s tick against a slow query that is
// not a rare interleaving. On a read failure the previous values
// simply persist until the next tick, which is the correct failure
// mode for a gauge: last known truth beats a confident zero.
func (e *PostureExporter) Export(ctx context.Context) error {
	start := time.Now()

	p, err := e.store.Posture(ctx)

	// Observed before the error check so a slow failure is measured
	// too. A store read that times out at 30s is the single most
	// useful sample the histogram can hold, and skipping it would make
	// the p99 look healthy precisely when it is not.
	metrics.PostureExportDurationSeconds.Observe(time.Since(start).Seconds())

	if err != nil {
		metrics.PostureExportTotal.WithLabelValues(outcomeError).Inc()

		return fmt.Errorf("posture export: %w", err)
	}

	metrics.ResetPosture()

	for _, c := range p.Actionable {
		metrics.ReposActionable.WithLabelValues(c.RuleName, c.Org).Set(float64(c.Count))
	}

	for _, c := range p.Tracked {
		metrics.ReposTracked.WithLabelValues(c.Org).Set(float64(c.Count))
	}

	for _, c := range p.Unmeasurable {
		metrics.ReposUnmeasurable.WithLabelValues(c.Org, c.Reason).Set(float64(c.Count))
	}

	metrics.PostureExportTotal.WithLabelValues(outcomeOK).Inc()

	e.logger.Debug("posture exported",
		"orgs", len(p.Tracked),
		"rule_series", len(p.Actionable),
		"unmeasurable_series", len(p.Unmeasurable),
		"tracked", totalOrgCount(p.Tracked),
		"unmeasurable", totalReasonCount(p.Unmeasurable),
	)

	return nil
}

func totalOrgCount(counts []store.OrgCount) int {
	total := 0
	for _, c := range counts {
		total += c.Count
	}

	return total
}

func totalReasonCount(counts []store.ReasonCount) int {
	total := 0
	for _, c := range counts {
		total += c.Count
	}

	return total
}
