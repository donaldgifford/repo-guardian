//go:build integration

package temporal_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"

	"github.com/donaldgifford/repo-guardian/internal/temporal"
	"github.com/donaldgifford/repo-guardian/internal/temporal/temporaltest"
)

const promoteTestWorkflow = "promote-test"

func promoteTestWorkflowFn(workflow.Context) (string, error) { return "ok", nil }

// TestPromoteBuild_DevServer pins the rc.1 stall fix against a real
// server: a versioned worker gets no tasks until PromoteBuild makes its
// build current, an older build then leaves it alone, and a newer build
// takes over.
func TestPromoteBuild_DevServer(t *testing.T) {
	t.Parallel()

	srv := temporaltest.Start(t)
	c := srv.Client
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	start := func(buildID string) {
		t.Helper()

		w := temporal.NewWorker(c, &temporal.WorkerConfig{TaskQueue: srv.Config.TaskQueue, ActivityConcurrency: 1, BuildID: buildID})
		w.RegisterWorkflowWithOptions(promoteTestWorkflowFn, workflow.RegisterOptions{Name: promoteTestWorkflow})

		if err := w.Start(); err != nil {
			t.Fatalf("start worker %s: %v", buildID, err)
		}
		t.Cleanup(w.Stop)
	}

	promote := func(buildID string) {
		t.Helper()

		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()

		if err := temporal.PromoteBuild(ctx, c, temporal.DeploymentRC, buildID, quiet); err != nil {
			t.Fatalf("PromoteBuild(%s): %v", buildID, err)
		}
	}

	start("2.0.0-rc.2")

	if err := temporal.RequireCurrentVersion(t.Context(), c, temporal.DeploymentRC, "2.0.0-rc.2"); err == nil {
		t.Fatal("RequireCurrentVersion before promotion = nil, want an error")
	}

	promote("2.0.0-rc.2")

	if err := temporal.RequireCurrentVersion(t.Context(), c, temporal.DeploymentRC, "2.0.0-rc.2"); err != nil {
		t.Fatalf("RequireCurrentVersion after promotion: %v", err)
	}

	// Promotion is what unblocks dispatch: the workflow completes.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: srv.Config.TaskQueue}, promoteTestWorkflow)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	var got string
	if err := run.Get(ctx, &got); err != nil || got != "ok" {
		t.Fatalf("workflow result = %q, %v; want \"ok\"", got, err)
	}

	// An older build (a crash-restarting pod mid-rollout) stands down.
	start("2.0.0-rc.1")
	promote("2.0.0-rc.1")

	if err := temporal.RequireCurrentVersion(t.Context(), c, temporal.DeploymentRC, "2.0.0-rc.2"); err != nil {
		t.Errorf("older build displaced the current one: %v", err)
	}

	if err := temporal.RequireCurrentVersion(t.Context(), c, temporal.DeploymentRC, "2.0.0-rc.1"); !errors.Is(err, temporal.ErrNotCurrent) {
		t.Errorf("RequireCurrentVersion(older) = %v, want ErrNotCurrent", err)
	}

	// A newer build wins.
	start("2.0.0")
	promote("2.0.0")

	if err := temporal.RequireCurrentVersion(t.Context(), c, temporal.DeploymentRC, "2.0.0"); err != nil {
		t.Errorf("newer build did not become current: %v", err)
	}
}
