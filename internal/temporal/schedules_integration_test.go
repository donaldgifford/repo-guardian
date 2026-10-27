//go:build integration

package temporal_test

import (
	"errors"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"

	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

// Schedule reconciliation (IMPL-0025 13.2): ensuring twice is a no-op,
// an interval change updates the spec in place, and removal is
// idempotent.
func TestSchedules_Reconcile(t *testing.T) {
	t.Parallel()

	srv := temporaltest.Start(t)
	c := srv.Client
	s := &temporal.Schedule{ID: "discovery", Every: time.Hour, Workflow: "DiscoveryWorkflow", TaskQueue: srv.Config.TaskQueue}

	every := func() (time.Duration, enumspb.ScheduleOverlapPolicy) {
		t.Helper()

		d, err := c.ScheduleClient().GetHandle(t.Context(), s.ID).Describe(t.Context())
		if err != nil {
			t.Fatalf("describe: %v", err)
		}

		return d.Schedule.Spec.Intervals[0].Every, d.Schedule.Policy.Overlap
	}

	for range 2 {
		if err := temporal.EnsureSchedule(t.Context(), c, s); err != nil {
			t.Fatalf("EnsureSchedule: %v", err)
		}
	}

	if got, overlap := every(); got != time.Hour || overlap != enumspb.SCHEDULE_OVERLAP_POLICY_SKIP {
		t.Errorf("schedule = every %v, overlap %v; want 1h, skip", got, overlap)
	}

	s.Every = 30 * time.Minute
	if err := temporal.EnsureSchedule(t.Context(), c, s); err != nil {
		t.Fatalf("EnsureSchedule after an interval change: %v", err)
	}

	if got, _ := every(); got != 30*time.Minute {
		t.Errorf("interval after change = %v, want 30m", got)
	}

	for range 2 {
		if err := temporal.RemoveSchedule(t.Context(), c, s.ID); err != nil {
			t.Fatalf("RemoveSchedule: %v", err)
		}
	}

	_, err := c.ScheduleClient().GetHandle(t.Context(), s.ID).Describe(t.Context())

	var notFound *serviceerror.NotFound
	if !errors.As(err, &notFound) {
		t.Errorf("describe after remove: %v, want NotFound", err)
	}

}
