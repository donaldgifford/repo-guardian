package temporal

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// DescribeBacklog reports the workflow task queue's oldest-task age and
// its poller count, for the API status page (DESIGN-0027). It is a
// read-only DescribeTaskQueue call.
func DescribeBacklog(ctx context.Context, c client.Client, cfg *Config) (age time.Duration, pollers int, err error) {
	resp, err := c.WorkflowService().DescribeTaskQueue(ctx, &workflowservice.DescribeTaskQueueRequest{
		Namespace:     cfg.Namespace,
		TaskQueue:     &taskqueuepb.TaskQueue{Name: cfg.TaskQueue, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		TaskQueueType: enumspb.TASK_QUEUE_TYPE_WORKFLOW,
		ReportStats:   true,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("describe task queue %s: %w", cfg.TaskQueue, err)
	}

	return resp.GetStats().GetApproximateBacklogAge().AsDuration(), len(resp.GetPollers()), nil
}
