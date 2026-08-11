// Compliance history for the report CLI. See DESIGN-0022 §Compliance
// snapshots and the per-org report and IMPL-0023 Phase 4.
//
// The posture gauges answer "how compliant is the fleet right now" and
// Prometheus keeps that answer for as long as its retention allows —
// typically weeks. The question this table exists for is "how compliant
// were we last quarter", which no metrics store configured for
// operational alerting can answer.
//
// Volume is tiny: orgs x rules rows per day, roughly 120/day at target
// scale, so no retention machinery ships. That is a decision to revisit
// only if the fleet grows by orders of magnitude, not a thing to build
// ahead of need.

package checker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// SnapshotTaker writes periodic compliance history.
type SnapshotTaker struct {
	store  store.Store
	logger *slog.Logger
	now    func() time.Time
}

// SnapshotTakerOptions bundles the SnapshotTaker constructor inputs.
type SnapshotTakerOptions struct {
	Store  store.Store
	Logger *slog.Logger

	// Now overrides the clock. Tests set it to write several dated
	// snapshots without sleeping; production leaves it nil.
	Now func() time.Time
}

// NewSnapshotTaker builds a SnapshotTaker.
func NewSnapshotTaker(opts SnapshotTakerOptions) *SnapshotTaker {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &SnapshotTaker{
		store:  opts.Store,
		logger: opts.Logger,
		now:    now,
	}
}

// Take records one dated snapshot of every (org, rule) pair.
//
// Register under the same leader election as stale-sweep and
// posture-export. Unlike those two, running this on every replica would
// not merely duplicate effort — it would corrupt the history, because
// each replica would insert its own rows at its own timestamp and a
// quarter-over-quarter query would count the same state N times, or
// once, depending on whether the clocks happened to agree.
//
// A failure is logged and returned, and nothing retries within the
// interval: a missed snapshot leaves a gap in a daily series, which is
// visible and harmless, while a retry loop against a struggling
// database is neither.
func (s *SnapshotTaker) Take(ctx context.Context) error {
	start := s.now()

	rows, err := s.store.InsertComplianceSnapshot(ctx, start)
	if err != nil {
		return fmt.Errorf("compliance snapshot: %w", err)
	}

	s.logger.Info("compliance snapshot recorded",
		"rows", rows,
		"snapshot_at", start.Format(time.RFC3339),
	)

	return nil
}
