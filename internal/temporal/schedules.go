package temporal

import (
	"context"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
)

// Schedule is a Temporal Schedule that starts Workflow every Every.
type Schedule struct {
	ID        string
	Every     time.Duration
	Workflow  string
	Args      []any
	TaskQueue string
	Priority  sdktemporal.Priority
}

// EnsureSchedule creates s, or brings an existing schedule's spec and
// action in line with it, so an interval change takes effect on the
// next worker start. Overlap is skip: a slow run is never doubled. Every
// worker calls it at startup; it is idempotent.
func EnsureSchedule(ctx context.Context, c client.Client, s *Schedule) error {
	spec := client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: s.Every}}}
	action := &client.ScheduleWorkflowAction{
		ID: s.ID, Workflow: s.Workflow, Args: s.Args, TaskQueue: s.TaskQueue, Priority: s.Priority,
	}

	_, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: s.ID, Spec: spec, Action: action, Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if err == nil {
		return nil
	}

	if !errors.Is(err, sdktemporal.ErrScheduleAlreadyRunning) {
		return fmt.Errorf("create schedule %s: %w", s.ID, err)
	}

	err = c.ScheduleClient().GetHandle(ctx, s.ID).Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			sched := in.Description.Schedule
			sched.Spec = &spec
			sched.Action = action

			if sched.Policy == nil {
				sched.Policy = &client.SchedulePolicies{}
			}

			sched.Policy.Overlap = enumspb.SCHEDULE_OVERLAP_POLICY_SKIP

			return &client.ScheduleUpdate{Schedule: &sched}, nil
		},
	})
	if err != nil {
		return fmt.Errorf("update schedule %s: %w", s.ID, err)
	}

	return nil
}

// RemoveSchedule deletes the schedule id; a missing one is not an error.
func RemoveSchedule(ctx context.Context, c client.Client, id string) error {
	err := c.ScheduleClient().GetHandle(ctx, id).Delete(ctx)

	var notFound *serviceerror.NotFound
	if err == nil || errors.As(err, &notFound) {
		return nil
	}

	return fmt.Errorf("delete schedule %s: %w", id, err)
}
