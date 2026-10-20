//go:build integration

package activities_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/activities"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
	"github.com/donaldgifford/repo-guardian/internal/workflows"
)

// standInRemediation is RemediationWorkflow's end-of-run shape from
// IMPL-0028's spike: take the starting signal, work briefly, drain the
// channel before completing, and report how many signals the run
// consumed.
func standInRemediation(ctx workflow.Context, _ *workflows.RemediationInput) error {
	ch := workflow.GetSignalChannel(ctx, workflows.RemediateSignal)

	var s workflows.Remediate

	ch.Receive(ctx, &s)
	n := 1

	if err := workflow.Sleep(ctx, 20*time.Millisecond); err != nil {
		return err
	}

	for ch.ReceiveAsync(&s) {
		n++
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})

	return workflow.ExecuteActivity(ctx, "count", n).Get(ctx, nil)
}

// TestIntegration_StartRemediationsIntoCompletingWorkflow is IMPL-0030
// 2.7: StartRemediations' signal-with-start (reuse ALLOW_DUPLICATE)
// against a workflow that keeps completing loses no signal, the result
// IMPL-0028's spike recorded for a draining run.
func TestIntegration_StartRemediationsIntoCompletingWorkflow(t *testing.T) {
	srv := temporaltest.Start(t)

	const queue = "repo-guardian-remediate-it"

	var consumed atomic.Int64

	w := worker.New(srv.Client, queue, worker.Options{})
	w.RegisterWorkflowWithOptions(standInRemediation, workflow.RegisterOptions{Name: workflows.RemediationWorkflowName})
	w.RegisterActivityWithOptions(func(_ context.Context, n int) error {
		consumed.Add(int64(n))

		return nil
	}, activity.RegisterOptions{Name: "count"})

	if err := w.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(w.Stop)

	starter := activities.NewRemediationStarter(srv.Client, queue, nil)
	start := []workflows.RemediationStart{{RepositoryID: 9, Control: "codeowners", InstallationID: 7, Priority: workflows.PrioritySchedule}}

	const senders, perSender = 4, 25

	var wg sync.WaitGroup
	for range senders {
		wg.Go(func() {
			for i := range perSender {
				if err := starter.StartRemediations(t.Context(), start); err != nil {
					t.Errorf("StartRemediations: %v", err)

					return
				}

				time.Sleep(time.Duration(5+(i*7)%20) * time.Millisecond)
			}
		})
	}

	wg.Wait()

	eventually(t, time.Minute, func() bool { return consumed.Load() == senders*perSender })
}
